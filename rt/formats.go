package rt

// The readers std/csv and std/xml stream through, as slots rt carries and
// never opens. internal/stdcsv and internal/stdxml build them over a
// Reader; rt names neither package.

// CsvRecords is std/csv_host's `Records`: a CSV reader over a Reader,
// which internal/stdcsv builds and reads one record at a time.
type CsvRecords struct{ records any }

// NewCsvRecords wraps records as a value.
func NewCsvRecords(records any) CsvRecords { return CsvRecords{records: records} }

// Records is the reader the value wraps.
func (c CsvRecords) Records() any { return c.records }

// XmlTokens is std/xml_host's `Tokens`: an XML token reader over a
// Reader, which internal/stdxml builds and reads one token at a time.
type XmlTokens struct{ tokens any }

// NewXmlTokens wraps tokens as a value.
func NewXmlTokens(tokens any) XmlTokens { return XmlTokens{tokens: tokens} }

// Tokens is the reader the value wraps.
func (x XmlTokens) Tokens() any { return x.tokens }
