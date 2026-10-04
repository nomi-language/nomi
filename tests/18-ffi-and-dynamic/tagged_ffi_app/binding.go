package taggedffiapp

import "strings"

type Box struct {
	Label string
}

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}

func MakeBox(label string) *Box {
	return &Box{Label: label}
}

func BoxLabel(box *Box) string {
	return box.Label
}
