//go:build integration

package sleep_test

import (
	"os"
	"path/filepath"
	"testing"
)

func writeContextSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"AGENT.md": "# Organization Agent\nFollow canonical organizational policies.\n",
		filepath.Join("ingenieria_ia", "AGENT.md"): "# Engineering Agent\nFollow department scope.\n",
		filepath.Join("ingenieria_ia", "orquestador", "PERFIL.md"): `---
departamento: ingenieria_ia
rol: orquestador
dominio_memoria: ingenieria_ia
agente_base: true
---
# Orquestador profile
Coordinate bounded engineering work and verify evidence.
`,
	}
	for relative, content := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create context fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write context fixture %s: %v", relative, err)
		}
	}
	return root
}
