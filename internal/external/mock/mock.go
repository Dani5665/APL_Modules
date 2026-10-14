// Package mock provides an in-memory external directory with seeded data, so
// the whole application can be developed, demoed and tested without MSSQL.
package mock

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"haynesproform/internal/external"
)

// Store names (MANDANT_NAME) and ids (MANDANT_ID) as they would appear in the
// external database. The local stores table must carry the same pairs.
const (
	StoreSofia   = "Магазин София"
	StoreVarna   = "Магазин Варна"
	StorePlovdiv = "Магазин Пловдив"

	MandantSofia   = "1"
	MandantVarna   = "2"
	MandantPlovdiv = "3"
)

// MandantID returns the MANDANT_ID of a mock store by its name.
func MandantID(storeName string) string { return mandantIDs[storeName] }

// Directory is a seeded, concurrency-safe implementation of
// external.Directory.
type Directory struct {
	mu      sync.RWMutex
	salers  map[string]external.Saler // keyed by lowercased login
	clients []external.Client
	logins  map[string][]string // client code (CUSTOMER_NUMBER) -> usernames
}

var _ external.Directory = (*Directory)(nil)

// New returns a directory seeded with the development data set: 3 stores,
// 2 salespeople and 20 clients with several usernames each.
func New() *Directory {
	d := &Directory{
		salers: map[string]external.Saler{},
		logins: map[string][]string{},
	}
	d.seed()
	return d
}

// clientSeed drives both the client list and the generated usernames.
type clientSeed struct {
	code   string
	name   string
	object string
	store  string
	users  int
}

var seedClients = []clientSeed{
	{"100000001", "Автосервиз Балкан ЕООД", "Сервиз Люлин", StoreSofia, 3},
	{"100000002", "Мото Партс БГ АД", "Централен склад", StoreSofia, 2},
	{"100000003", "Ди Ем Ауто ЕООД", "Магазин Надежда", StoreSofia, 4},
	{"100000004", "Кар Сервиз Плюс ООД", "Сервиз Младост", StoreSofia, 1},
	{"100000005", "Ауто Комерс 2000 ЕООД", "Офис София", StoreSofia, 2},
	{"100000006", "Технокар Груп АД", "Сервиз Обеля", StoreSofia, 3},
	{"100000007", "Профи Ауто ЕООД", "Магазин Витоша", StoreSofia, 2},
	{"100000008", "Морски Мотор ООД", "Сервиз Аспарухово", StoreVarna, 2},
	{"100000009", "Варна Ауто Център АД", "Централен обект", StoreVarna, 5},
	{"100000010", "Черно Море Транс ЕООД", "База Владислав", StoreVarna, 1},
	{"100000011", "Приморски Части ООД", "Магазин Чайка", StoreVarna, 3},
	{"100000012", "Ауто Бавария Варна ЕООД", "Сервиз Западна промишлена зона", StoreVarna, 2},
	{"100000013", "Галакси Моторс ООД", "Шоурум Варна", StoreVarna, 2},
	{"100000014", "Тракия Ауто ЕООД", "Сервиз Тракия", StorePlovdiv, 3},
	{"100000015", "Пловдив Парт Сървиз АД", "Склад Кючук Париж", StorePlovdiv, 2},
	{"100000016", "Родопи Кар ООД", "Магазин Смирненски", StorePlovdiv, 1},
	{"100000017", "Марица Ауто Хаус ЕООД", "Обект Марица", StorePlovdiv, 4},
	{"100000018", "Бул Моторс Пловдив ООД", "Сервиз Северен", StorePlovdiv, 2},
	{"100000019", "Стандарт Ауто ЕООД", "Магазин Централен", StorePlovdiv, 2},
	{"100000020", "Експрес Части БГ ООД", "Склад Тракия", StorePlovdiv, 3},
}

// usernamePrefixes give each client's logins a readable, stable shape.
var usernamePrefixes = []string{"office", "servis", "sklad", "shef", "tehnik"}

func (d *Directory) seed() {
	// Salespeople. These LOGIN values are what arrives in the entry link.
	for _, s := range []external.Saler{
		{Login: "ivan.petrov", Store: StoreSofia},
		{Login: "maria.dimitrova", Store: StoreVarna},
	} {
		d.salers[strings.ToLower(s.Login)] = s
	}

	for _, c := range seedClients {
		d.clients = append(d.clients, external.Client{
			Code: c.code, Name: c.name, Object: c.object, Store: c.store, MandantID: mandantIDs[c.store],
		})
		users := make([]string, 0, c.users)
		for i := 0; i < c.users; i++ {
			users = append(users, fmt.Sprintf("%s%s", usernamePrefixes[i%len(usernamePrefixes)], c.code))
		}
		sort.Strings(users)
		d.logins[c.code] = users
	}
}

// GetSaler implements external.Directory.
func (d *Directory) GetSaler(ctx context.Context, login string) (*external.Saler, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	s, ok := d.salers[strings.ToLower(strings.TrimSpace(login))]
	if !ok {
		return nil, external.ErrNotFound
	}
	out := s
	return &out, nil
}

// GetClientByCode implements external.Directory.
func (d *Directory) GetClientByCode(ctx context.Context, code string) (*external.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	code = strings.TrimSpace(code)
	for _, c := range d.clients {
		if c.Code == code {
			out := c
			return &out, nil
		}
	}
	return nil, external.ErrNotFound
}

// ListClientLogins implements external.Directory.
func (d *Directory) ListClientLogins(ctx context.Context, clientCode string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	users := d.logins[strings.TrimSpace(clientCode)]
	return append([]string(nil), users...), nil
}

// ListClientsByStores implements external.Directory.
func (d *Directory) ListClientsByStores(ctx context.Context, mandantIDs []string) ([]external.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(mandantIDs) == 0 {
		return nil, nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	want := make(map[string]bool, len(mandantIDs))
	for _, v := range mandantIDs {
		want[strings.ToLower(strings.TrimSpace(v))] = true
	}

	var out []external.Client
	for _, c := range d.clients {
		if want[strings.ToLower(c.MandantID)] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Code < out[j].Code
	})
	return out, nil
}

// Ping implements external.Directory. The mock is always reachable.
func (d *Directory) Ping(context.Context) error { return nil }

// Close implements external.Directory.
func (d *Directory) Close() error { return nil }

// Clients returns a copy of the seeded clients, for tests and fixtures.
func (d *Directory) Clients() []external.Client {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]external.Client(nil), d.clients...)
}

// noProfileCustomerCode is a seeded customer that has no catalog profile, to
// exercise the "no profile" state.
const noProfileCustomerCode = "100000099"

// customers builds the customer information list from the seeded clients,
// with some contact fields left empty on purpose.
func (d *Directory) customers() []external.Customer {
	var out []external.Customer
	for i, c := range d.clients {
		cu := external.Customer{Code: c.Code, Name: c.Name, Mandant: c.Store}
		if i%3 != 2 {
			cu.Address = fmt.Sprintf("гр. %s, ул. Примерна %d", map[string]string{
				StoreSofia: "София", StoreVarna: "Варна", StorePlovdiv: "Пловдив"}[c.Store], 10+i)
		}
		if i%4 != 3 {
			cu.Phone = fmt.Sprintf("088 %03d %04d", 100+i, 1000+i*7)
		}
		if i%5 != 4 {
			cu.AccountablePerson = []string{"Иван Иванов", "Мария Петрова", "Георги Димитров"}[i%3]
		}
		out = append(out, cu)
	}
	out = append(out, external.Customer{Code: noProfileCustomerCode, Name: "Клиент без профил ЕООД"})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// ListCustomers implements external.Directory.
func (d *Directory) ListCustomers(ctx context.Context) ([]external.Customer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.customers(), nil
}

// GetCustomer implements external.Directory.
func (d *Directory) GetCustomer(ctx context.Context, code string) (*external.Customer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	code = strings.TrimSpace(code)
	for _, c := range d.customers() {
		if c.Code == code {
			out := c
			return &out, nil
		}
	}
	return nil, external.ErrNotFound
}

// ListProfiles implements external.Directory. Every username of a seeded
// client becomes a profile; the last one of each client is inactive and the
// first has never logged in.
func (d *Directory) ListProfiles(ctx context.Context, code string) ([]external.Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	code = strings.TrimSpace(code)
	var store string
	for _, c := range d.clients {
		if c.Code == code {
			store = c.Store
		}
	}
	users := d.logins[code]
	out := make([]external.Profile, 0, len(users))
	for i, u := range users {
		p := external.Profile{
			Login: u, Password: "Parola" + code[len(code)-4:] + fmt.Sprint(i), Mandant: store,
			Active: !(len(users) > 1 && i == len(users)-1),
		}
		if i > 0 {
			p.LastLogin = time.Date(2026, 9, 1+i, 9+i, 30, 0, 0, time.UTC)
		}
		out = append(out, p)
	}
	return out, nil
}

// mandantIDs are the mock stores' ids in the transport view.
var mandantIDs = map[string]string{StoreSofia: MandantSofia, StoreVarna: MandantVarna, StorePlovdiv: MandantPlovdiv}

// transports are the seeded transport options per mandant id.
var transports = map[string][]string{
	"1": {"Куриер Спиди", "Лична доставка", "Собствен транспорт"},
	"2": {"Куриер Еконт", "Лична доставка"},
	// Plovdiv has none, to exercise an empty list.
}

// LoginExists implements external.Directory: a login is taken when any seeded
// client or salesperson uses it.
func (d *Directory) LoginExists(ctx context.Context, login string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	login = strings.ToLower(strings.TrimSpace(login))
	if _, ok := d.salers[login]; ok {
		return true, nil
	}
	for _, users := range d.logins {
		for _, u := range users {
			if strings.ToLower(u) == login {
				return true, nil
			}
		}
	}
	return false, nil
}

// ListTransports implements external.Directory.
func (d *Directory) ListTransports(ctx context.Context, mandantID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]string(nil), transports[strings.TrimSpace(mandantID)]...), nil
}
