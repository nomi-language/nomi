package format

import "testing"

// A keyword declaration prints each part it was written with, and is a
// fixed point.
func TestFormat_KeywordDeclRoundTrips(t *testing.T) {
	for _, src := range []string{
		"/// Items in the cart.\npub message cart_items(count: Int) `{$count :integer} items`\n",
		"database main\n",
		"message hello() `Hello`\n",
		"query user_by_email(email: String) -> Maybe<User> `select 1`\n",
		"noun item `{{item}}`\n",
		"message status(state: Status) `\n    .input {$state :string}\n    .match $state\n    * {{Done}}\n    `\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
		if err := SameMeaning(src, src); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFormat_KeywordDeclNormalizesSpacing(t *testing.T) {
	formatsKeepingMeaning(t,
		"pub   message  cart_items(  count:Int ,)   `{$count} items`\n",
		"pub message cart_items(count: Int) `{$count} items`\n")
}
