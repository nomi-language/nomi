package inlinegoblockapp

import "strings"

type Box struct {
	label string
}

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}

func MakeBox(label string) *Box {
	box := new(Box)
	box.label = label
	return box
}

func BoxLabel(box *Box) string {
	if box == nil {
		return ""
	}
	return box.label
}
