// Package mock provides an in-memory external directory with seeded data, so
// the whole application can be developed, demoed and tested without MSSQL.
package mock

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"haynesproform/internal/external"
)

// Store values as they would appear in the external database. The local
// stores table must carry these as external_value.
const (
	StoreSofia   = "SOFIA"
	StoreVarna   = "VARNA"
	StorePlovdiv = "PLOVDIV"
)

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
			Code: c.code, Name: c.name, Object: c.object, Store: c.store,
		})
		users := make([]string, 0, c.users)
		for i := 0; i < c.users; i++ {
			users = append(users, fmt.Sprintf("%s_%s", usernamePrefixes[i%len(usernamePrefixes)], c.code))
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
func (d *Directory) ListClientsByStores(ctx context.Context, storeValues []string) ([]external.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(storeValues) == 0 {
		return nil, nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	want := make(map[string]bool, len(storeValues))
	for _, v := range storeValues {
		want[strings.ToLower(strings.TrimSpace(v))] = true
	}

	var out []external.Client
	for _, c := range d.clients {
		if want[strings.ToLower(c.Store)] {
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
