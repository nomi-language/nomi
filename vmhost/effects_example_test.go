package vmhost_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// A host runs a pricing rule its users write, and lets the rule reach
// nothing. The rule loads and answers a value; the
// same rule with a network call is refused when it loads, before anything
// runs.
func ExampleWithEffects() {
	rule := `struct Order
    items: Int
    loyal: Bool
end

fn discount(order: Order) -> Decimal
    if order.loyal and order.items >= 10 then 0.15d else 0.00d
end
`
	p, err := vmhost.LoadSource("rule", rule, vmhost.WithEffects())
	if err != nil {
		fmt.Println(err)
		return
	}
	rate, err := p.Call(context.Background(), "discount", vmhost.Fields{"items": 12, "loyal": true})
	fmt.Println(rt.DisplayText(rate), err)

	fetching := strings.Replace(rule, "    if order.loyal",
		"    _ = http.get(Url\"https://rates.example.com/today\")\n    if order.loyal", 1)
	_, err = vmhost.LoadSource("rule", "import\n    std::http\n    std::url.Url\nend\n\n"+fetching, vmhost.WithEffects())
	first, _, _ := strings.Cut(err.Error(), "\n")
	fmt.Println(first)
	// Output:
	// 0.15 <nil>
	// rule.nomi:12:9: this program has the effect std::http, and the host allows no effects
}
