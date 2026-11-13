package rt

import "reflect"

// StdHostType names one of std's `host type`s: its declaring module and its
// name, as the front end identifies it.
type StdHostType struct {
	Origin, Name string
}

// The names of std's `host type`s whose values are rt's own Go structs
// (std/io's Reader, std/http_host's Client, ...). Such a type declares no
// `impl Debug`, so its Debug is the universal default, the bare type name
// (spec §12 *Display and Debug*), at the top and nested; its Display, which
// a values: row reads, is `<Name>`, as a HostHandle's is. Dynamic, Context
// and Supervisor are absent: Dynamic's Debug is std's impl, and the other
// two render as their Opaque text.
//
// internal/irbuild's TestStdHostTypesRenderByName holds this table to
// irbuild's stdHostSpecs: every row there whose Go type is an rt struct
// with no Debug of its own is named here, by the same name.
var hostTypeNames = map[reflect.Type]StdHostType{
	reflect.TypeFor[Reader]():         {"std/io", "Reader"},
	reflect.TypeFor[Writer]():         {"std/io", "Writer"},
	reflect.TypeFor[File]():           {"std/io", "File"},
	reflect.TypeFor[FileSystem]():     {"std/fs_host", "FileSystem"},
	reflect.TypeFor[ChildProcess]():   {"std/process_host", "Child"},
	reflect.TypeFor[ProcessFake]():    {"std/process_host", "Fake"},
	reflect.TypeFor[HTTPClient]():     {"std/http_host", "Client"},
	reflect.TypeFor[HTTPExchange]():   {"std/http_host", "Exchange"},
	reflect.TypeFor[HTTPListener]():   {"std/http_host", "Listener"},
	reflect.TypeFor[HTTPIncoming]():   {"std/http_host", "Incoming"},
	reflect.TypeFor[TLSCertificate](): {"std/http_host", "Certificate"},
	reflect.TypeFor[TLSConfig]():      {"std/http_host", "TlsConfig"},
	reflect.TypeFor[NetConn]():        {"std/http_host", "Conn"},
	reflect.TypeFor[NetListener]():    {"std/http_host", "TcpListener"},
	reflect.TypeFor[HTTPMux]():        {"std/http_host", "Mux"},
	reflect.TypeFor[HTTPJar]():        {"std/http_host", "Jar"},
	reflect.TypeFor[HTTPHandle]():     {"std/http_host", "Handle"},
	reflect.TypeFor[CsvRecords]():     {"std/csv_host", "Records"},
	reflect.TypeFor[XmlTokens]():      {"std/xml_host", "Tokens"},
	reflect.TypeFor[Regex]():          {"std/regex", "Regex"},
	reflect.TypeFor[BoolTrue]():       {"std/bool", "True"},
	reflect.TypeFor[BoolFalse]():      {"std/bool", "False"},
}

// stdHostValueTypes are the host types in hostTypeNames that are values,
// with equality: a Regex compares by its pattern (HostKeyed), and True and
// False are Bool's.
var stdHostValueTypes = map[StdHostType]bool{
	{"std/regex", "Regex"}: true,
	{"std/bool", "True"}:   true,
	{"std/bool", "False"}:  true,
}

// stdEngineHandleTypes are std's handles that are not one of rt's plain
// structs: values the engine holds as an Opaque (a task, a channel half, a
// select arm, a Context, a Supervisor).
var stdEngineHandleTypes = []StdHostType{
	{"std/tasks", "Task"},
	{"std/channels", "Sender"},
	{"std/channels", "Receiver"},
	{"std/channels", "Arm"},
	{"std/context", "Context"},
	{"std/supervisors", "Supervisor"},
}

// stdDataValueTypes are std's host types that are values held as their
// own rt type rather than through hostTypeNames: std/dynamic's Dynamic, a
// JSON-like tree compared and hashed by its contents (DynamicEqual).
var stdDataValueTypes = []StdHostType{
	{"std/dynamic", "Dynamic"},
}

// StdHostValueTypes are std's host types that are values, with equality:
// those of hostTypeNames in stdHostValueTypes, and stdDataValueTypes.
// Every other std host type is one of StdHandleTypes.
func StdHostValueTypes() []StdHostType {
	out := append([]StdHostType(nil), stdDataValueTypes...)
	for _, t := range hostTypeNames {
		if stdHostValueTypes[t] {
			out = append(out, t)
		}
	}
	return out
}

// StdHandleTypes are std's handles to running state or to a resource
// outside the program: every std host type that is not a value
// (StdHostValueTypes). None has
// equality (spec §5 *Equality*): rt answers none for one, so the checker
// rejects `==` over one, a Set element or Map key of one, and an Equatable
// or Hashable bound it would meet, as for a function. The checker reads
// this list (internal/analysis's stdHandleTypes), and internal/irbuild's
// TestStdHandleTypesCoverTheHostSpecs fails when a std host type is
// neither here nor a value.
func StdHandleTypes() []StdHostType {
	out := append([]StdHostType(nil), stdEngineHandleTypes...)
	for _, t := range hostTypeNames {
		if !stdHostValueTypes[t] {
			out = append(out, t)
		}
	}
	return out
}

// HostTypeName is the std type name of v when v is one of hostTypeNames'
// rt structs.
func HostTypeName(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	t, ok := hostTypeNames[reflect.TypeOf(v)]
	return t.Name, ok
}
