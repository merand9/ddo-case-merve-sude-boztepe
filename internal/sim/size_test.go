package sim

import "testing"

func TestFullSizeStats(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ds := Generate(DefaultConfig(42, anchor))
	t.Logf("donors=%d donations=%d products=%d events=%d requests=%d dist=%d transf=%d inelig=%d",
		len(ds.Donors), len(ds.Donations), len(ds.Products), len(ds.Events), len(ds.Requests), len(ds.Distributions), len(ds.Transfusions), ds.Ineligible)
	st := map[string]int{}
	for _, p := range ds.Products {
		st[p.Component+":"+p.Status]++
	}
	t.Log(st)
}
