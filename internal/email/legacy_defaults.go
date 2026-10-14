package email

// previousDefault is a subject and body that an earlier version shipped as a
// template's default.
type previousDefault struct {
	Subject string
	Body    string
}

// previousDefaults lists, per template key, the texts earlier versions
// shipped. A stored template that still equals one of them was never edited
// by an admin, so SeedTemplates upgrades it to the current default; anything
// else is an admin's own text and is left alone.
//
// When a shipped default changes, add the old text here.
var previousDefaults = map[string][]previousDefault{
	TemplateRequestCreated: {
		{
			Subject: "Ново запитване №{{request_id}} - {{client_name}}",
			Body: `<p>Подадено е ново запитване за активация на модули.</p>
<table>
  <tr><td><strong>Номер на запитване:</strong></td><td>{{request_id}}</td></tr>
  <tr><td><strong>Дата на подаване:</strong></td><td>{{request_date}}</td></tr>
  <tr><td><strong>Клиентски номер:</strong></td><td>{{client_code}}</td></tr>
  <tr><td><strong>Име на клиент:</strong></td><td>{{client_name}}</td></tr>
  <tr><td><strong>Обект на клиента:</strong></td><td>{{client_object}}</td></tr>
  <tr><td><strong>Магазин:</strong></td><td>{{client_store}}</td></tr>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Тест период:</strong></td><td>{{test_period}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация до:</strong></td><td>{{end_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
  <tr><td><strong>Подал запитването:</strong></td><td>{{submitter_email}}</td></tr>
</table>
<p><a href="{{request_link}}">Отвори запитването в админ панела</a></p>`,
		},
		{
			Subject: "Ново запитване за NEXT модули - {{client_name}}",
			Body: `<p>Подадено е ново запитване за активация на модули в каталога.</p>
<table>
  <tr><td><strong>Номер на запитване:</strong></td><td>{{request_id}}</td></tr>
  <tr><td><strong>Дата на подаване:</strong></td><td>{{request_date}}</td></tr>
  <tr><td><strong>Клиентски номер:</strong></td><td>{{client_code}}</td></tr>
  <tr><td><strong>Име на клиент:</strong></td><td>{{client_name}}</td></tr>
  <tr><td><strong>Обект на клиента:</strong></td><td>{{client_object}}</td></tr>
  <tr><td><strong>Магазин:</strong></td><td>{{client_store}}</td></tr>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Тест период:</strong></td><td>{{test_period}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация до:</strong></td><td>{{end_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
  <tr><td><strong>Подал запитването:</strong></td><td>{{submitter_email}}</td></tr>
</table>
<p><a href="{{request_link}}">Отвори запитването в админ панела</a></p>`,
		},
	},
	TemplateRequestDenied: {
		{
			Subject: "Запитване №{{request_id}} е отказано",
			Body: `<p>Здравейте,</p>
<p>Вашето запитване №{{request_id}} от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>отказано</strong>.</p>
<table>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
</table>
<p><strong>Коментар:</strong> {{admin_comment}}</p>
<p>Поздрави,<br>{{decided_by}}</p>`,
		},
		{
			// The new subject with the body that still named the request number.
			Subject: "Запитване за активация на модули - {{client_name}} е отказано",
			Body: `<p>Здравейте,</p>
<p>Вашето запитване №{{request_id}} от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>отказано</strong>.</p>
<table>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
</table>
<p><strong>Коментар:</strong> {{admin_comment}}</p>
<p>Поздрави,<br>{{decided_by}}</p>`,
		},
	},
	TemplateRequestApproved: {
		{
			Subject: "Запитване №{{request_id}} е одобрено",
			Body: `<p>Здравейте,</p>
<p>Вашето запитване №{{request_id}} от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>одобрено</strong>.</p>
<table>
  <tr><td><strong>Обект на клиента:</strong></td><td>{{client_object}}</td></tr>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Тест период:</strong></td><td>{{test_period}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация до:</strong></td><td>{{end_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
</table>
<p><strong>Коментар:</strong> {{admin_comment}}</p>
<p>Поздрави,<br>{{decided_by}}</p>`,
		},
	},
}
