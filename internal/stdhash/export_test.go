package stdhash

// AlgorithmNames is the table's names in code order, for the test that
// holds them to std/hash.nomi's `Algorithm`.
func AlgorithmNames() []string {
	names := make([]string, len(algorithms))
	for i, a := range algorithms {
		names[i] = a.name
	}
	return names
}
