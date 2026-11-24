package diagnosis

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func TestBuildDiagnosis(t *testing.T) {
	cases := []struct {
		name, log, profile, py string
		confidence             Confidence
		field, value           string // sugestão esperada; "" = nenhuma
		mention                string
	}{
		{"openssl no node", "#13 1.7 Error: error:0308010C:digital envelope routines::unsupported\n#13 1.7   code: 'ERR_OSSL_EVP_UNSUPPORTED'", "P-NODE", "", Confirmed, domain.FieldBuildLegacyOpenSSL, "true", "0308010C"},
		{"openssl no estático", "Error: error:0308010C:digital envelope routines::unsupported", "P-STATIC", "", Confirmed, domain.FieldBuildLegacyOpenSSL, "true", "OpenSSL"},
		{"openssl não vale para Python", "ERR_OSSL_EVP_UNSUPPORTED", "P-PY", "3.13", Unknown, "", "", ""},
		{"distutils no 3.13", "ModuleNotFoundError: No module named 'distutils'", "P-PY", "3.13", Confirmed, domain.FieldPythonVersion, "3.11", "distutils"},
		{"distutils já no 3.11: sem sugestão", "ModuleNotFoundError: No module named 'distutils'", "P-PY", "3.11", Confirmed, "", "", "distutils"},
		{"wheel de pacote antigo", "ERROR: Failed building wheel for pillow\nERROR: Could not build wheels for pillow", "P-PY", "3.13", Possible, domain.FieldPythonVersion, "3.12", "pillow"},
		{"nenhuma distribuição: diz o log, sem sugerir Python", "ERROR: No matching distribution found for uvicorn==0.14.0", "P-PY", "3.12", Possible, "", "", "uvicorn"},
		{"o legado desce até o 3.8", "Failed building wheel for numpy", "P-PY", "3.9", Possible, domain.FieldPythonVersion, "3.8", "numpy"},
		{"no 3.8 não há mais abaixo", "Failed building wheel for numpy", "P-PY", "3.8", Possible, "", "", "numpy"},
		{"log sem assinatura", "npm ERR! code ELIFECYCLE", "P-NODE", "", Unknown, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Build(c.log, c.profile, c.py)
			if d.Confidence != c.confidence {
				t.Fatalf("confidence = %s, want %s (%+v)", d.Confidence, c.confidence, d)
			}
			if c.value == "" {
				if d.Suggestion != nil {
					t.Fatalf("unexpected suggestion: %+v", d.Suggestion)
				}
			} else if d.Suggestion == nil || d.Suggestion.Overrides[c.field] != c.value {
				t.Fatalf("suggestion = %+v, want %s=%s", d.Suggestion, c.field, c.value)
			}
			if c.mention != "" && !strings.Contains(d.Message, c.mention) {
				t.Errorf("the message must cite the log line (%q): %s", c.mention, d.Message)
			}
		})
	}
}
