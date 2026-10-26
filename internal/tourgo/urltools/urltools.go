package urltools

import "net/url"

type ParsedURL struct {
	Scheme   string
	Host     string
	Path     string
	RawQuery string
	Query    map[string][]string
}

func EscapeQuery(value string) string {
	return url.QueryEscape(value)
}

func UnescapeQuery(value string) (string, error) {
	return url.QueryUnescape(value)
}

func ParseQuery(raw string) (map[string][]string, error) {
	return url.ParseQuery(raw)
}

func Parse(raw string) (ParsedURL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ParsedURL{}, err
	}
	return ParsedURL{
		Scheme:   parsed.Scheme,
		Host:     parsed.Host,
		Path:     parsed.Path,
		RawQuery: parsed.RawQuery,
		Query:    map[string][]string(parsed.Query()),
	}, nil
}
