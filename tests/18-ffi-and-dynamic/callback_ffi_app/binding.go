package callbackffiapp

func ApplyTwice(s string, f func(string) (string, error)) (string, error) {
	first, err := f(s)
	if err != nil {
		return "", err
	}
	return f(first)
}
