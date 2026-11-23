package domain

import "testing"

func TestStackSlugFromRepository(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/ocnaibill/fec":          "fec",
		"https://github.com/ocnaibill/Meu_Site.git": "meu-site",
		"git@github.com:org/caderno.git":            "caderno",
		"https://github.com/org/---":                "projeto",
	} {
		if got := StackSlugFromRepository(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

// O frontend, o que se visita, fica com o nome do projeto; o backend com -api.
func TestProposeAppSlugs(t *testing.T) {
	apps := []StackScanApp{{RootDirectory: "", Role: CompBackend}, {RootDirectory: "frontend", Role: CompFrontend}, {RootDirectory: "tools/admin", Role: CompApp}}
	ProposeAppSlugs("fec", apps, func(s string) bool { return s == "fec-admin" })
	if apps[1].Slug != "fec" || apps[0].Slug != "fec-api" || apps[2].Slug != "fec-admin-2" {
		t.Errorf("slugs = %q %q %q", apps[0].Slug, apps[1].Slug, apps[2].Slug)
	}
	one := []StackScanApp{{Role: CompBackend}}
	ProposeAppSlugs("loja", one, func(s string) bool { return s == "loja" })
	if one[0].Slug != "loja-2" {
		t.Errorf("uma só, nome em uso: %q", one[0].Slug)
	}
	// Sem frontend, o backend é o que se visita.
	two := []StackScanApp{{Role: CompBackend}, {RootDirectory: "worker", Role: CompApp}}
	ProposeAppSlugs("api", two, nil)
	if two[0].Slug != "api" || two[1].Slug != "api-worker" {
		t.Errorf("sem frontend: %q %q", two[0].Slug, two[1].Slug)
	}
}
