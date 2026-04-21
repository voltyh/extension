package version

import "strings"

const Current = "0.2.0"

var Build = Current

func Label() string {
	label := strings.TrimSpace(Build)
	if label == "" {
		label = Current
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "\t", "-", "\n", "-")
	return replacer.Replace(label)
}

func DefaultExportArchive() string {
	return "gemini-export-" + Label() + ".zip"
}
