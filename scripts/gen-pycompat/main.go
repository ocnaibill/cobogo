//go:build ignore

// Gera internal/analyzer/pycompat.json (DEC-54 A): para cada pacote de uma lista
// fechada e cada release, a menor e a maior versão do Python que ele declara nos
// classificadores do PyPI (guardadas só nos pontos em que o intervalo muda). É dado público
// do PyPI, não de nenhum repositório de teste: rode `go run scripts/gen-pycompat/main.go`
// para atualizar. Só pacotes que o Cobogó vê fixados com `==` em requirements.txt.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A lista é fechada e por critério: o framework web mais usado e os pacotes com código
// nativo que mais quebram quando o Python é mais novo que a versão fixada.
var packages = []string{
	"django", "pillow", "numpy", "pandas", "scipy", "psycopg2", "psycopg2-binary", "lxml",
	"cryptography", "cffi", "greenlet", "pyyaml", "matplotlib", "scikit-learn", "mysqlclient",
	"grpcio", "gevent", "uvloop", "pydantic-core", "orjson", "ujson", "bcrypt", "pyzmq",
	"regex", "markupsafe", "aiohttp", "multidict", "yarl", "frozenlist",
}

var (
	series  = regexp.MustCompile(`^(\d+)\.(\d+)`)
	pyClass = regexp.MustCompile(`^Programming Language :: Python :: (3\.\d+)$`)
	pre     = regexp.MustCompile(`[a-zA-Z]`)
)

type releaseInfo struct {
	Info struct {
		RequiresPython string   `json:"requires_python"`
		Classifiers    []string `json:"classifiers"`
	} `json:"info"`
}

func get(url string, v any) error {
	var last error
	for i := 0; i < 4; i++ {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			defer resp.Body.Close()
			return json.NewDecoder(resp.Body).Decode(v)
		}
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			last = fmt.Errorf("%s: %s", url, resp.Status)
			if resp.StatusCode == 404 {
				return last
			}
		} else {
			last = err
		}
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	return last
}

func minor(v string) int {
	p := strings.SplitN(v, ".", 2)
	a, _ := strconv.Atoi(p[0])
	b, _ := strconv.Atoi(p[1])
	return a*100 + b
}

func main() {
	out := map[string]map[string][2]string{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, pkg := range packages {
		var project struct {
			Releases map[string][]struct {
				Yanked bool `json:"yanked"`
			} `json:"releases"`
		}
		if err := get("https://pypi.org/pypi/"+pkg+"/json", &project); err != nil {
			fmt.Fprintln(os.Stderr, "ignorado:", pkg, err)
			continue
		}
		var versions []string
		for v, files := range project.Releases {
			if pre.MatchString(v) || len(files) == 0 || files[0].Yanked || !series.MatchString(v) {
				continue
			}
			versions = append(versions, v)
		}
		sort.Slice(versions, func(a, b int) bool { return less(versions[a], versions[b]) })
		ranges := make([][3]string, len(versions)) // versão, mínimo, máximo
		for i, v := range versions {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, pkg, v string) {
				defer wg.Done()
				defer func() { <-sem }()
				var r releaseInfo
				if err := get("https://pypi.org/pypi/"+pkg+"/"+v+"/json", &r); err != nil {
					return
				}
				lo, hi := "", ""
				for _, c := range r.Info.Classifiers {
					if m := pyClass.FindStringSubmatch(c); m != nil {
						if lo == "" || minor(m[1]) < minor(lo) {
							lo = m[1]
						}
						if hi == "" || minor(m[1]) > minor(hi) {
							hi = m[1]
						}
					}
				}
				ranges[i] = [3]string{v, lo, hi}
			}(i, pkg, v)
		}
		wg.Wait()
		// Só os pontos em que o intervalo muda: a versão fixada usa a entrada mais próxima abaixo dela.
		out[pkg] = map[string][2]string{}
		var prev [2]string
		for _, r := range ranges {
			if r[0] == "" || (r[1] == "" && r[2] == "") {
				continue
			}
			cur := [2]string{r[1], r[2]}
			if cur != prev {
				out[pkg][r[0]] = cur
				prev = cur
			}
		}
	}
	wg.Wait()
	doc := map[string]any{
		"generated": time.Now().UTC().Format("2006-01-02"),
		"source":    "classificadores do PyPI do primeiro release de cada série (major.minor)",
		"packages":  out,
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b, _ := json.MarshalIndent(doc, "", " ")
	if err := os.WriteFile("internal/analyzer/pycompat.json", b, 0o644); err != nil {
		panic(err)
	}
	total := 0
	for _, k := range keys {
		total += len(out[k])
		fmt.Printf("%s: %d séries\n", k, len(out[k]))
	}
	fmt.Println("total", total)
}

func less(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}
