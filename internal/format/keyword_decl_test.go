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
		"pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`select 1`\n",
		"sqlkit.database! main\n",
		"/// How big a coffee is.\npub choices.one_of! Size `small medium large`\n",
		"one_of! Size `small`\n",
		"kit.record! Point(x: Int, y: Int)\n",
		"pub query! names Sql`SELECT name FROM users`\n",
		"sqlkit.query! orders(user_id: Int) Sql`\n    SELECT id\n    FROM orders\n    WHERE user_id = @user_id\n    `\n",
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

func TestFormat_MacroDecl(t *testing.T) {
	src := "/// Tables from CSV.\npub macro table: CsvTable\n"
	if got := formatTwice(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
	formatsKeepingMeaning(t, "pub  macro table :CsvTable\n", "pub macro table: CsvTable\n")
}

func TestFormat_KeywordDeclNormalizesSpacing(t *testing.T) {
	formatsKeepingMeaning(t,
		"pub   message  cart_items(  count:Int ,)   `{$count} items`\n",
		"pub message cart_items(count: Int) `{$count} items`\n")
	formatsKeepingMeaning(t,
		"pub  sqlkit . query!  q( id:Int )   Sql`select 1`\n",
		"pub sqlkit.query! q(id: Int) Sql`select 1`\n")
}
