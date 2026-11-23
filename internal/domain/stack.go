package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// Os nomes de um projeto e das aplicações dele (DEC-52 item 1). O slug da aplicação é
// o endereço dela (<slug>.<domínio>), então o frontend, o que se visita, fica com o
// nome do projeto e o backend com <projeto>-api.

var notSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// Slugify reduz um nome a letras minúsculas, números e hífen, com até max caracteres.
func Slugify(s string, max int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("_", "-", ".", "-", " ", "-").Replace(s)
	s = notSlug.ReplaceAllString(s, "")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	return s
}

// StackSlugFromRepository propõe o nome do projeto a partir do link do repositório
// (o último trecho, sem .git).
func StackSlugFromRepository(repositoryURL string) string {
	u := strings.TrimSuffix(strings.TrimRight(repositoryURL, "/"), ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	if s := Slugify(u, 40); s != "" {
		return s
	}
	return "projeto"
}

// ProposeAppSlugs dá um slug a cada aplicação proposta: uma sozinha fica com o nome do
// projeto; com várias, o primeiro frontend fica com ele, o primeiro backend com
// <projeto>-api e as demais com <projeto>-<pasta>. taken diz se um slug já está em uso
// fora deste projeto; o que colide ganha -2, -3...
func ProposeAppSlugs(stack string, apps []StackScanApp, taken func(string) bool) {
	used := map[string]bool{}
	free := func(want string) string { return UniqueSlug(want, stack, used, taken) }
	if len(apps) == 1 {
		apps[0].Slug = free(stack)
		return
	}
	front, back := -1, -1
	for i, a := range apps {
		switch {
		case a.Role == CompFrontend && front < 0:
			front = i
		case (a.Role == CompBackend || a.Role == CompFullstack) && back < 0:
			back = i
		}
	}
	// Sem frontend, o backend é o que se visita.
	if front < 0 && back >= 0 {
		front, back = back, -1
	}
	if front >= 0 {
		apps[front].Slug = free(stack)
	}
	if back >= 0 {
		apps[back].Slug = free(stack + "-api")
	}
	for i := range apps {
		if i == front || i == back {
			continue
		}
		apps[i].Slug = free(stack + "-" + dirName(apps[i].RootDirectory))
	}
}

// ProposeAddedAppSlugs dá slugs às aplicações novas de um projeto que já tem outras:
// cada uma fica com <projeto>-<pasta>.
func ProposeAddedAppSlugs(stack string, apps []StackScanApp, taken func(string) bool) {
	used := map[string]bool{}
	for i := range apps {
		apps[i].Slug = UniqueSlug(stack+"-"+dirName(apps[i].RootDirectory), stack, used, taken)
	}
}

func dirName(dir string) string {
	if j := strings.LastIndex(dir, "/"); j >= 0 {
		dir = dir[j+1:]
	}
	if dir == "" {
		return "app"
	}
	return dir
}

// UniqueSlug devolve want (reduzido a slug) ou, se ele já está em used ou em uso
// (taken), want-2, want-3... e o marca em used.
func UniqueSlug(want, fallback string, used map[string]bool, taken func(string) bool) string {
	want = Slugify(want, 63)
	if want == "" {
		want = Slugify(fallback, 63)
	}
	cand := want
	for n := 2; used[cand] || (taken != nil && taken(cand)); n++ {
		suffix := "-" + strconv.Itoa(n)
		base := want
		if len(base)+len(suffix) > 63 {
			base = strings.Trim(base[:63-len(suffix)], "-")
		}
		cand = base + suffix
	}
	used[cand] = true
	return cand
}

// RoleLabel é o nome de um papel de aplicação no painel e nas mensagens.
func RoleLabel(role string) string {
	switch role {
	case CompFrontend:
		return "Frontend"
	case CompBackend:
		return "Backend"
	case CompFullstack:
		return "Aplicação web"
	}
	return "Aplicação"
}
