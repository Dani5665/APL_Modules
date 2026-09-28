package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wneessen/go-mail"

	"haynesproform/internal/auth"
	"haynesproform/internal/store"
)

// Security selects the transport encryption for the SMTP connection.
type Security string

const (
	SecurityNone     Security = "none"
	SecuritySTARTTLS Security = "starttls"
	SecurityTLS      Security = "tls"
)

// Label returns the Bulgarian label of a security mode.
func (s Security) Label() string {
	switch s {
	case SecuritySTARTTLS:
		return "STARTTLS"
	case SecurityTLS:
		return "SSL/TLS"
	default:
		return "Няма"
	}
}

// SecurityOptions are the choices offered in the SMTP settings form.
var SecurityOptions = []Security{SecurityNone, SecuritySTARTTLS, SecurityTLS}

// Settings is the SMTP configuration as stored in the settings table.
type Settings struct {
	Host     string
	Port     int
	Security Security
	Username string
	// Password is the decrypted password. It is never rendered back into the
	// settings form; an empty value on save means "leave unchanged".
	Password    string
	FromAddress string
	FromName    string
	// NotifyRecipients receive the "new request" email.
	NotifyRecipients []string
}

// Configured reports whether enough is set to attempt a send.
func (s Settings) Configured() bool {
	return strings.TrimSpace(s.Host) != "" && s.Port > 0 && strings.TrimSpace(s.FromAddress) != ""
}

// ErrNotConfigured is returned when SMTP has not been set up yet.
var ErrNotConfigured = errors.New("SMTP не е конфигуриран")

// LoadSettings reads and decrypts the SMTP configuration.
func LoadSettings(ctx context.Context, db *store.DB, enc *auth.Encrypter) (Settings, error) {
	raw, err := db.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}

	port, _ := strconv.Atoi(raw[store.KeySMTPPort])
	s := Settings{
		Host:             raw[store.KeySMTPHost],
		Port:             port,
		Security:         Security(raw[store.KeySMTPSecurity]),
		Username:         raw[store.KeySMTPUsername],
		FromAddress:      raw[store.KeySMTPFromAddress],
		FromName:         raw[store.KeySMTPFromName],
		NotifyRecipients: store.SplitEmails(raw[store.KeyNotifyRecipients]),
	}
	if s.Security == "" {
		s.Security = SecurityNone
	}

	if sealed := raw[store.KeySMTPPasswordEnc]; sealed != "" {
		pw, err := enc.Decrypt([]byte(sealed))
		if err != nil {
			return s, err
		}
		s.Password = pw
	}
	return s, nil
}

// Message is a ready-to-send email.
type Message struct {
	To         []string
	Subject    string
	BodyHTML   string
	BodyText   string
	Attachment *Attachment
}

// Attachment is a file sent with a message.
type Attachment struct {
	Filename string
	Content  []byte
}

// Sender delivers messages over SMTP.
type Sender struct {
	settings Settings
}

// NewSender builds a sender for the given settings.
func NewSender(s Settings) *Sender { return &Sender{settings: s} }

// dialTimeout bounds a delivery attempt, so a hanging SMTP server cannot
// stall the outbox worker indefinitely.
const dialTimeout = 30 * time.Second

// Send delivers one message.
func (s *Sender) Send(ctx context.Context, m Message) error {
	if !s.settings.Configured() {
		return ErrNotConfigured
	}
	if len(m.To) == 0 {
		return errors.New("няма получатели")
	}

	client, err := s.client()
	if err != nil {
		return err
	}
	defer client.Close()

	msg, err := s.buildMsg(m)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("изпращането е неуспешно: %w", err)
	}
	return nil
}

func (s *Sender) client() (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(s.settings.Port),
		mail.WithTimeout(dialTimeout),
	}

	switch s.settings.Security {
	case SecurityTLS:
		opts = append(opts, mail.WithSSL(), mail.WithTLSPolicy(mail.TLSMandatory))
	case SecuritySTARTTLS:
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	default:
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}

	if s.settings.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(s.settings.Username),
			mail.WithPassword(s.settings.Password),
		)
	} else {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthNoAuth))
	}

	client, err := mail.NewClient(s.settings.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("SMTP клиентът не може да бъде създаден: %w", err)
	}
	return client, nil
}

func (s *Sender) buildMsg(m Message) (*mail.Msg, error) {
	msg := mail.NewMsg()

	if s.settings.FromName != "" {
		if err := msg.FromFormat(s.settings.FromName, s.settings.FromAddress); err != nil {
			return nil, fmt.Errorf("невалиден подател: %w", err)
		}
	} else if err := msg.From(s.settings.FromAddress); err != nil {
		return nil, fmt.Errorf("невалиден подател: %w", err)
	}

	if err := msg.To(m.To...); err != nil {
		return nil, fmt.Errorf("невалиден получател: %w", err)
	}
	msg.Subject(m.Subject)
	msg.SetMessageID()

	text := m.BodyText
	if text == "" {
		text = PlainText(m.BodyHTML)
	}
	// Plain text first with the HTML as the alternative, which is the order
	// mail clients expect in a multipart/alternative message.
	msg.SetBodyString(mail.TypeTextPlain, text)
	msg.AddAlternativeString(mail.TypeTextHTML, m.BodyHTML)

	if m.Attachment != nil && len(m.Attachment.Content) > 0 {
		if err := msg.AttachReader(m.Attachment.Filename, bytes.NewReader(m.Attachment.Content)); err != nil {
			return nil, fmt.Errorf("прикаченият файл не може да бъде добавен: %w", err)
		}
	}
	return msg, nil
}
