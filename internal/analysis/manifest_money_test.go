package analysis

import (
	"reflect"
	"testing"
)

// [money].custom_currencies lists custom currencies as their literals write
// them, each once; a missing table or an empty list lists none, and an
// entry that is not a custom currency is an error naming it.
func TestParseManifestData_MoneyCustomCurrencies(t *testing.T) {
	const head = "[module]\nname = \"app\"\n\n[money]\n"
	m, err := ParseManifestData([]byte(head+"custom_currencies = [\"PTS:0\", \"BTC:8\"]\n"), "nomi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PTS:0", "BTC:8"}; !reflect.DeepEqual(m.CustomCurrencies, want) {
		t.Errorf("CustomCurrencies = %q, want %q", m.CustomCurrencies, want)
	}
	if _, listed := m.Tables["money"]; listed {
		t.Error("[money] is kept as a macro's table; it is Nomi's own")
	}
	for _, body := range []string{"", "custom_currencies = []\n"} {
		m, err := ParseManifestData([]byte(head+body), "nomi.toml")
		if err != nil || len(m.CustomCurrencies) != 0 {
			t.Errorf("%q: %q, %v; want no custom currency", body, m.CustomCurrencies, err)
		}
	}
	for _, c := range []struct{ list, want string }{
		{`["PTS"]`, "nomi.toml: [money] custom_currencies: \"PTS\" is not a custom currency: `PTS` names no minor digits: write the code, a colon and the digits, as PTS:2"},
		{`["pts:0"]`, "nomi.toml: [money] custom_currencies: \"pts:0\" is not a custom currency: `pts` is not a currency code: a code is 1 to 12 of A-Z and 0-9, starting with a letter"},
		{`["EUR:2"]`, "nomi.toml: [money] custom_currencies: \"EUR:2\" is not a custom currency: `EUR` is an ISO 4217 currency, which is not a custom one"},
		{`["EUR"]`, "nomi.toml: [money] custom_currencies: \"EUR\" is not a custom currency: `EUR` is an ISO 4217 currency, which is not a custom one"},
		{`["PTS:19"]`, "nomi.toml: [money] custom_currencies: \"PTS:19\" is not a custom currency: a currency has 0 to 18 minor digits, not 19"},
		{`["PTS:x"]`, "nomi.toml: [money] custom_currencies: \"PTS:x\" is not a custom currency: `x` is not a count of minor digits: write a whole number from 0 to 18, as PTS:2"},
		{`["PTS:00"]`, "nomi.toml: [money] custom_currencies: \"PTS:00\" is not a custom currency: `00` is not a count of minor digits: write a whole number from 0 to 18, as PTS:2"},
		{`["PTS:0", "BTC:8", "PTS:0"]`, "nomi.toml: [money] custom_currencies lists PTS:0 twice"},
	} {
		_, err := ParseManifestData([]byte(head+"custom_currencies = "+c.list+"\n"), "nomi.toml")
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v\nwant %s", c.list, err, c.want)
		}
	}
}
