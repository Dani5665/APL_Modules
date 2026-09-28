package export

import (
	"bytes"
	"database/sql"
	"testing"

	"github.com/xuri/excelize/v2"

	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

func sampleRows() []store.Activation {
	return []store.Activation{
		{
			ID: 1, ClientCode: "100000001", ClientName: "Автосервиз Балкан ЕООД",
			ClientObject: "Сервиз Люлин", ClientStore: "Магазин София",
			Username: "office_100000001", Module: modules.FastCalculator,
			StartDate: "2026-09-01", EndDate: "2026-11-30",
		},
		{
			ID: 2, ClientCode: "100000001", ClientName: "Автосервиз Балкан ЕООД",
			ClientObject: "Сервиз Люлин", ClientStore: "Магазин София",
			Username: "office_100000001", Module: modules.HaynesPro, Tier: modules.TierUltra,
			StartDate: "2026-09-01", EndDate: "2027-08-31",
		},
	}
}

func TestBuildProducesAReadableWorkbook(t *testing.T) {
	res, err := Build(sampleRows(), "2026-09-17", Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2", res.RowCount)
	}
	if res.Filename != "aktivni_moduli_2026-09-17.xlsx" {
		t.Errorf("Filename = %q", res.Filename)
	}

	f, err := excelize.OpenReader(bytes.NewReader(res.Content))
	if err != nil {
		t.Fatalf("the generated file is not a readable workbook: %v", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) != 1 || sheets[0] != sheetName {
		t.Fatalf("sheets = %v, want exactly [%s]", sheets, sheetName)
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (header plus two activations)", len(rows))
	}

	for i, want := range headers {
		if rows[0][i] != want {
			t.Errorf("header[%d] = %q, want %q", i, rows[0][i], want)
		}
	}

	first := rows[1]
	wantFirst := []string{
		"100000001", "Автосервиз Балкан ЕООД", "Сервиз Люлин", "Магазин София",
		"office_100000001", "Fast Calculator", "", "01.09.2026", "30.11.2026", "Активен",
	}
	for i, want := range wantFirst {
		if first[i] != want {
			t.Errorf("row 1 column %d (%s) = %q, want %q", i, headers[i], first[i], want)
		}
	}

	if got := rows[2][6]; got != "Ultra" {
		t.Errorf("tier cell = %q, want %q", got, "Ultra")
	}
}

func TestBuildWritesRealDates(t *testing.T) {
	res, err := Build(sampleRows(), "2026-09-17", Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(res.Content))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()

	// A real date cell parses back as a time, unlike a formatted string.
	got, err := f.GetCellValue(sheetName, "H2", excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("GetCellValue: %v", err)
	}
	if got == "01.09.2026" {
		t.Error("the start date was written as text, not as a real Excel date")
	}

	styleID, err := f.GetCellStyle(sheetName, "H2")
	if err != nil {
		t.Fatalf("GetCellStyle: %v", err)
	}
	style, err := f.GetStyle(styleID)
	if err != nil {
		t.Fatalf("GetStyle: %v", err)
	}
	if style.CustomNumFmt == nil || *style.CustomNumFmt != "dd.mm.yyyy" {
		t.Errorf("date cell number format = %v, want dd.mm.yyyy", style.CustomNumFmt)
	}
}

func TestBuildHandlesAnEmptyReport(t *testing.T) {
	res, err := Build(nil, "2026-09-17", Options{})
	if err != nil {
		t.Fatalf("Build with no rows: %v", err)
	}
	if res.RowCount != 0 {
		t.Errorf("RowCount = %d, want 0", res.RowCount)
	}

	f, err := excelize.OpenReader(bytes.NewReader(res.Content))
	if err != nil {
		t.Fatalf("the empty report is not a readable workbook: %v", err)
	}
	defer f.Close()

	rows, err := f.GetRows(sheetName)
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want only the header", len(rows))
	}
}

func TestBuildGroupedByClient(t *testing.T) {
	rows := append(sampleRows(), store.Activation{
		ID: 3, ClientCode: "100000002", ClientName: "Гараж Изток ООД",
		ClientObject: "Магазин Варна", ClientStore: "Магазин Варна",
		Username: "garage_2", Module: modules.HaynesPro, Tier: modules.TierBusiness,
		StartDate: "2026-09-01", EndDate: "2026-12-31",
	})

	res, err := Build(rows, "2026-09-17", Options{GroupByClient: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.RowCount != 3 {
		t.Errorf("RowCount = %d, want 3 activations (headings are not counted)", res.RowCount)
	}
	if res.Filename != "aktivni_moduli_po_klienti_2026-09-17.xlsx" {
		t.Errorf("Filename = %q", res.Filename)
	}

	f, err := excelize.OpenReader(bytes.NewReader(res.Content))
	if err != nil {
		t.Fatalf("the grouped report is not a readable workbook: %v", err)
	}
	defer f.Close()

	got, err := f.GetRows(sheetName)
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	// Header, client 1 heading, two activations, client 2 heading, one activation.
	want := [][]string{
		groupedHeaders,
		{"100000001 — Автосервиз Балкан ЕООД   ·   Обект: Сервиз Люлин   ·   Магазин: Магазин София"},
		{"office_100000001", "Fast Calculator", "", "01.09.2026", "30.11.2026", "Активен"},
		{"office_100000001", "HaynesPro", "Ultra", "01.09.2026", "31.08.2027", "Активен"},
		// The store equals the object here, so it is not repeated.
		{"100000002 — Гараж Изток ООД   ·   Обект: Магазин Варна"},
		{"garage_2", "HaynesPro", "Business", "01.09.2026", "31.12.2026", "Активен"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		for j := range want[i] {
			if j >= len(got[i]) || got[i][j] != want[i][j] {
				t.Errorf("row %d = %q, want %q", i+1, got[i], want[i])
				break
			}
		}
	}

	merged, err := f.GetMergeCells(sheetName)
	if err != nil {
		t.Fatalf("GetMergeCells: %v", err)
	}
	if len(merged) != 2 {
		t.Errorf("got %d merged ranges, want one per client heading", len(merged))
	}

	// Activation rows are outlined under their heading so Excel can collapse them.
	level, err := f.GetRowOutlineLevel(sheetName, 3)
	if err != nil {
		t.Fatalf("GetRowOutlineLevel: %v", err)
	}
	if level != 1 {
		t.Errorf("activation row outline level = %d, want 1", level)
	}
}

func TestBuildStatusColumnReflectsTheDate(t *testing.T) {
	rows := []store.Activation{
		{
			ID: 1, ClientCode: "100000001", ClientName: "A", ClientObject: "O", ClientStore: "S",
			Username: "u", Module: modules.FastCalculator,
			StartDate: "2026-01-01", EndDate: "2026-01-31",
		},
		{
			ID: 2, ClientCode: "100000002", ClientName: "B", ClientObject: "O", ClientStore: "S",
			Username: "u", Module: modules.FastCalculator,
			StartDate: "2026-01-01", EndDate: "2026-12-31",
			RevokedAt: sql.NullString{String: "2026-02-01T00:00:00Z", Valid: true},
		},
	}

	res, err := Build(rows, "2026-09-17", Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	f, _ := excelize.OpenReader(bytes.NewReader(res.Content))
	defer f.Close()

	got, _ := f.GetRows(sheetName)
	if got[1][9] != "Изтекъл" {
		t.Errorf("status of a past activation = %q, want Изтекъл", got[1][9])
	}
	if got[2][9] != "Прекратен" {
		t.Errorf("status of a revoked activation = %q, want Прекратен", got[2][9])
	}
}
