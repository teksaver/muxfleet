package components

import (
	"embed"
	"fmt"
	"io/fs"
)

// FS embeds all component apply scripts (AD-22).
//
//go:embed */apply.sh
var FS embed.FS

// GetComponentScript returns the bash script contents for the named component.
func GetComponentScript(name string) ([]byte, error) {
	path := fmt.Sprintf("%s/apply.sh", name)
	data, err := FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("composant %q non trouvé dans les scripts embarqués: %w", name, err)
	}
	return data, nil
}

// ListComponents returns the list of embedded component names.
func ListComponents() ([]string, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
