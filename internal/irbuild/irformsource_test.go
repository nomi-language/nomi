package irbuild

// irFormSource wraps a body in the smallest program that lowers.
func irFormSource(body string) string {
	return "import std/io\n\n" + body + "\nfn main() {\n  io.print(\"go\")\n}\n"
}

func slicesContain(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
