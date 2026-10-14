package external

import "testing"

func TestFirstPerCodeKeepsTheFirstRowOfEachCustomer(t *testing.T) {
	rows := []Customer{
		{Code: "1", Name: "first", Phone: "111"},
		{Code: "2", Name: "other"},
		{Code: "1", Name: "second", Phone: "222"},
	}
	got := FirstPerCode(rows)
	if len(got) != 2 || got[0].Name != "first" || got[0].Phone != "111" || got[1].Code != "2" {
		t.Fatalf("FirstPerCode = %+v", got)
	}
}
