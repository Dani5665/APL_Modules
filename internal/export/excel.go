// Package export builds the Excel report of currently active module
// activations. The same generator serves the manual download and the
// scheduled email, so the two can never differ.
package export

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

// sheetName is the single worksheet of the report.
const sheetName = "Активни модули"

// headers are the Bulgarian column titles, in the required order.
var headers = []string{
	"Клиентски номер",
	"Име на клиент",
	"Обект на клиента",
	"Магазин",
	"Потребител",
	"Модул",
	"Ниво",
	"От дата",
	"До дата",
	"Статус",
}

// columnWidths are sized to the content rather than autofitted, which the
// streaming writer cannot do.
var columnWidths = []float64{18, 32, 28, 20, 24, 18, 12, 13, 13, 12}

// groupedHeaders are the columns of the grouped layout. The client's own
// details move into a heading row above its activations.
var groupedHeaders = []string{
	"Потребител",
	"Модул",
	"Ниво",
	"От дата",
	"До дата",
	"Статус",
}

// groupedColumnWidths are wider in the first column than in the flat layout
// because the merged client heading starts there.
var groupedColumnWidths = []float64{30, 18, 12, 13, 13, 12}

// Options select the report's layout.
type Options struct {
	// GroupByClient puts each client's details in a heading row, followed by
	// one row per activation of that client.
	GroupByClient bool
}

// Result is a generated report.
type Result struct {
	Filename string
	Content  []byte
	// RowCount is the number of activations in the report, used for the
	// {{active_count}} placeholder.
	RowCount int
}

// Generator builds reports from the local database.
type Generator struct {
	db *store.DB
}

// NewGenerator builds the export generator.
func NewGenerator(db *store.DB) *Generator { return &Generator{db: db} }

// Filename returns the report's file name for a given business date.
func Filename(today string) string { return "aktivni_moduli_" + today + ".xlsx" }

// GroupedFilename returns the file name of the grouped report, so the two
// layouts downloaded on the same day do not overwrite each other.
func GroupedFilename(today string) string {
	return "aktivni_moduli_po_klienti_" + today + ".xlsx"
}

// Generate builds the report of activations that are active today.
func (g *Generator) Generate(ctx context.Context, opts Options) (*Result, error) {
	rows, err := g.db.ListActiveActivations(ctx)
	if err != nil {
		return nil, fmt.Errorf("read active activations: %w", err)
	}
	return Build(rows, dates.Today(), opts)
}

// styles are the cell styles shared by both layouts.
type styles struct {
	header     int
	date       int
	clientHead int
	indented   int
}

// Build renders the given activations into a workbook. today decides how the
// status column is computed.
func Build(rows []store.Activation, today string, opts Options) (*Result, error) {
	f := excelize.NewFile()
	defer f.Close()

	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, fmt.Errorf("create sheet: %w", err)
	}
	f.SetActiveSheet(index)
	// NewFile always starts with a default sheet that is not wanted.
	if err := f.DeleteSheet("Sheet1"); err != nil {
		return nil, fmt.Errorf("remove default sheet: %w", err)
	}

	st, err := newStyles(f)
	if err != nil {
		return nil, err
	}

	sw, err := f.NewStreamWriter(sheetName)
	if err != nil {
		return nil, fmt.Errorf("create stream writer: %w", err)
	}

	cols, widths := headers, columnWidths
	if opts.GroupByClient {
		cols, widths = groupedHeaders, groupedColumnWidths
	}

	for i, w := range widths {
		if err := sw.SetColWidth(i+1, i+1, w); err != nil {
			return nil, fmt.Errorf("set column width: %w", err)
		}
	}

	// Freeze the header row so it stays visible while scrolling.
	if err := sw.SetPanes(&excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
		Selection: []excelize.Selection{
			{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"},
		},
	}); err != nil {
		return nil, fmt.Errorf("freeze header row: %w", err)
	}

	headerRow := make([]any, len(cols))
	for i, h := range cols {
		headerRow[i] = excelize.Cell{StyleID: st.header, Value: h}
	}
	if err := sw.SetRow("A1", headerRow, excelize.RowOpts{Height: 20}); err != nil {
		return nil, fmt.Errorf("write header row: %w", err)
	}

	if opts.GroupByClient {
		err = writeGrouped(sw, rows, today, st)
	} else {
		err = writeFlat(sw, rows, today, st)
	}
	if err != nil {
		return nil, err
	}

	if err := sw.Flush(); err != nil {
		return nil, fmt.Errorf("flush stream writer: %w", err)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write workbook: %w", err)
	}

	name := Filename(today)
	if opts.GroupByClient {
		name = GroupedFilename(today)
	}
	return &Result{
		Filename: name,
		Content:  buf.Bytes(),
		RowCount: len(rows),
	}, nil
}

// writeFlat writes one row per activation, with the client's details
// repeated on every row, as a filterable Excel table.
func writeFlat(sw *excelize.StreamWriter, rows []store.Activation, today string, st styles) error {
	for i, a := range rows {
		start, end, err := activationDates(a)
		if err != nil {
			return err
		}

		cells := []any{
			a.ClientCode,
			a.ClientName,
			a.ClientObject,
			a.ClientStore,
			a.Username,
			modules.Label(a.Module),
			modules.TierLabel(a.Module, a.Tier),
			excelize.Cell{StyleID: st.date, Value: asExcelTime(start)},
			endCell(a, end, st),
			a.StatusOn(today).Label(),
		}
		cell, err := excelize.CoordinatesToCellName(1, i+2)
		if err != nil {
			return err
		}
		if err := sw.SetRow(cell, cells); err != nil {
			return fmt.Errorf("write row %d: %w", i+2, err)
		}
	}

	// An auto-filter needs at least the header row plus one data row.
	if len(rows) == 0 {
		return nil
	}
	lastCell, err := excelize.CoordinatesToCellName(len(headers), len(rows)+1)
	if err != nil {
		return err
	}
	if err := sw.AddTable(&excelize.Table{
		Range:             "A1:" + lastCell,
		Name:              "AktivniModuli",
		StyleName:         "TableStyleLight9",
		ShowRowStripes:    boolPtr(true),
		ShowFirstColumn:   false,
		ShowLastColumn:    false,
		ShowColumnStripes: false,
	}); err != nil {
		return fmt.Errorf("add auto-filter: %w", err)
	}
	return nil
}

// writeGrouped writes a heading row per client, followed by that client's
// activations. The activation rows sit one outline level below the heading,
// so Excel can collapse each client to its heading.
//
// It relies on the rows arriving ordered by client, which
// ListActiveActivations guarantees. A client split across the list would get
// two headings.
func writeGrouped(sw *excelize.StreamWriter, rows []store.Activation, today string, st styles) error {
	lastCol := len(groupedHeaders)
	rowNum := 2
	prevClient := ""

	for i, a := range rows {
		if i == 0 || a.ClientCode != prevClient {
			prevClient = a.ClientCode

			first, err := excelize.CoordinatesToCellName(1, rowNum)
			if err != nil {
				return err
			}
			last, err := excelize.CoordinatesToCellName(lastCol, rowNum)
			if err != nil {
				return err
			}
			// Every cell of the merged range carries the style, so the fill
			// and border cover the whole heading and not just its first cell.
			heading := make([]any, lastCol)
			heading[0] = excelize.Cell{StyleID: st.clientHead, Value: clientHeading(a)}
			for c := 1; c < lastCol; c++ {
				heading[c] = excelize.Cell{StyleID: st.clientHead}
			}
			if err := sw.SetRow(first, heading, excelize.RowOpts{Height: 20}); err != nil {
				return fmt.Errorf("write client heading at row %d: %w", rowNum, err)
			}
			if err := sw.MergeCell(first, last); err != nil {
				return fmt.Errorf("merge client heading at row %d: %w", rowNum, err)
			}
			rowNum++
		}

		start, end, err := activationDates(a)
		if err != nil {
			return err
		}
		cells := []any{
			excelize.Cell{StyleID: st.indented, Value: a.Username},
			modules.Label(a.Module),
			modules.TierLabel(a.Module, a.Tier),
			excelize.Cell{StyleID: st.date, Value: asExcelTime(start)},
			endCell(a, end, st),
			a.StatusOn(today).Label(),
		}
		cell, err := excelize.CoordinatesToCellName(1, rowNum)
		if err != nil {
			return err
		}
		if err := sw.SetRow(cell, cells, excelize.RowOpts{OutlineLevel: 1}); err != nil {
			return fmt.Errorf("write row %d: %w", rowNum, err)
		}
		rowNum++
	}
	return nil
}

// clientHeading is the text of a client's heading row in the grouped layout.
// The store is left out when it matches the object, which is true for every
// client today because both come from MANDANT_NAME.
func clientHeading(a store.Activation) string {
	parts := []string{a.ClientCode + " — " + a.ClientName}
	if a.ClientObject != "" {
		parts = append(parts, "Обект: "+a.ClientObject)
	}
	if a.ClientStore != "" && a.ClientStore != a.ClientObject {
		parts = append(parts, "Магазин: "+a.ClientStore)
	}
	return strings.Join(parts, "   ·   ")
}

func activationDates(a store.Activation) (time.Time, time.Time, error) {
	start, err := dates.ParseISO(a.StartDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("activation %d has an invalid start date: %w", a.ID, err)
	}
	end, err := dates.ParseISO(a.EndDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("activation %d has an invalid end date: %w", a.ID, err)
	}
	return start, end, nil
}

func newStyles(f *excelize.File) (styles, error) {
	var st styles
	var err error

	st.header, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E8EDF3"}},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: false},
		Border:    bottomBorder(),
	})
	if err != nil {
		return st, fmt.Errorf("create header style: %w", err)
	}

	// A custom number format keeps the cells real dates while displaying them
	// the Bulgarian way, so sorting and filtering in Excel still work.
	st.date, err = f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("dd.mm.yyyy")})
	if err != nil {
		return st, fmt.Errorf("create date style: %w", err)
	}

	st.clientHead, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#F4F6F9"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
		Border:    []excelize.Border{{Type: "top", Color: "#9AA7B5", Style: 1}},
	})
	if err != nil {
		return st, fmt.Errorf("create client heading style: %w", err)
	}

	st.indented, err = f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Indent: 1}})
	if err != nil {
		return st, fmt.Errorf("create indented style: %w", err)
	}
	return st, nil
}

// asExcelTime returns a value excelize writes as a real date cell.
func asExcelTime(t time.Time) time.Time {
	// The workbook carries no time zone, so the date is written as the plain
	// calendar day it represents in Sofia.
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func bottomBorder() []excelize.Border {
	return []excelize.Border{{Type: "bottom", Color: "#9AA7B5", Style: 1}}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// endCell is the "До дата" cell: a date, or the text "Без крайна дата" for an
// activation without an end date.
func endCell(a store.Activation, end time.Time, st styles) excelize.Cell {
	if a.EndDate == dates.NoEndDate {
		return excelize.Cell{Value: dates.NoEndDateLabel}
	}
	return excelize.Cell{StyleID: st.date, Value: asExcelTime(end)}
}
