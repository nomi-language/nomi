package stdstrings

import (
	"slices"
	"testing"
)

func TestWords(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"_- \t\n", nil},
		{"a", []string{"a"}},
		{"A", []string{"A"}},
		{"user_id", []string{"user", "id"}},
		{"user-id", []string{"user", "id"}},
		{"user id", []string{"user", "id"}},
		{"__user--id  ", []string{"user", "id"}},
		{"userId", []string{"user", "Id"}},
		{"UserID", []string{"User", "ID"}},
		{"HTTPServer", []string{"HTTP", "Server"}},
		{"parseHTTP", []string{"parse", "HTTP"}},
		{"parseHTTP2Request", []string{"parse", "HTTP2", "Request"}},
		{"v2Api", []string{"v2", "Api"}},
		{"v2_api", []string{"v2", "api"}},
		{"2fa", []string{"2fa"}},
		{"route66", []string{"route66"}},
		{"ABC", []string{"ABC"}},
		{"Hello World", []string{"Hello", "World"}},
		{"émileZola", []string{"émile", "Zola"}},
		{"ÉCOLENormale", []string{"ÉCOLE", "Normale"}},
		{"straßeName", []string{"straße", "Name"}},
		{"ǅemalBey", []string{"ǅemal", "Bey"}},
		{"東京_tower", []string{"東京", "tower"}},
		{"a.b", []string{"a.b"}},
	}
	for _, tt := range tests {
		if got := Words(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("Words(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCaseStyles(t *testing.T) {
	tests := []struct {
		in, snake, kebab, pascal, camel string
	}{
		{"", "", "", "", ""},
		{"__--  ", "", "", "", ""},
		{"a", "a", "a", "A", "a"},
		{"A", "a", "a", "A", "a"},
		{"user_id", "user_id", "user-id", "UserId", "userId"},
		{"userId", "user_id", "user-id", "UserId", "userId"},
		{"HTTPServer", "http_server", "http-server", "HttpServer", "httpServer"},
		{"parseHTTP", "parse_http", "parse-http", "ParseHttp", "parseHttp"},
		{"parseHTTP2Request", "parse_http2_request", "parse-http2-request", "ParseHttp2Request", "parseHttp2Request"},
		{"v2Api", "v2_api", "v2-api", "V2Api", "v2Api"},
		{"  Hello   World  ", "hello_world", "hello-world", "HelloWorld", "helloWorld"},
		{"ÉCOLE normale", "école_normale", "école-normale", "ÉcoleNormale", "écoleNormale"},
		{"ǆemal bey", "ǆemal_bey", "ǆemal-bey", "ǅemalBey", "ǆemalBey"},
		{"straße name", "straße_name", "straße-name", "StraßeName", "straßeName"},
		{"ΟΔΟΣ ΜΕΓΑΛΗ", "οδος_μεγαλη", "οδος-μεγαλη", "ΟδοςΜεγαλη", "οδοςΜεγαλη"},
	}
	for _, tt := range tests {
		for _, c := range []struct {
			name string
			f    func(string) string
			want string
		}{
			{"SnakeCase", SnakeCase, tt.snake},
			{"KebabCase", KebabCase, tt.kebab},
			{"PascalCase", PascalCase, tt.pascal},
			{"CamelCase", CamelCase, tt.camel},
		} {
			if got := c.f(tt.in); got != c.want {
				t.Errorf("%s(%q) = %q, want %q", c.name, tt.in, got, c.want)
			}
		}
	}
}
