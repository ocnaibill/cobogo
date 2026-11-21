package planner

import "testing"

func TestShellStartExecsOnlyWhenItIsSafe(t *testing.T) {
	for in, want := range map[string]string{
		// Um comando simples: a aplicação vira o processo 1 e recebe o sinal.
		"bun run src/server.ts":       "exec bun run src/server.ts",
		"node server.js":              "exec node server.js",
		"npm start":                   "exec npm start",
		"  node  server.js  ":         "exec node  server.js",
		`node server.js --msg "a  b"`: `exec node server.js --msg "a  b"`,
		// Uma cadeia com &&: só o último comando vira a aplicação.
		"npm run migrate && node server.js": "npm run migrate && exec node server.js",
		"a && b && node s.js":               "a && b && exec node s.js",
		// Atribuições na frente ficam antes do exec.
		"NODE_ENV=production node app.js": "NODE_ENV=production exec node app.js",
		"A=1 B=two node app.js":           "A=1 B=two exec node app.js",
		"cd api && node server.js":        "cd api && exec node server.js",
		// Em dúvida, fica como está (o shell continua como antes).
		"node a.js | tee log":      "node a.js | tee log",
		"node a.js & node b.js":    "node a.js & node b.js",
		"node a.js; node b.js":     "node a.js; node b.js",
		"node a.js > out.log":      "node a.js > out.log",
		"node $(pick).js":          "node $(pick).js",
		"node `pick`.js":           "node `pick`.js",
		"if true; then node a; fi": "if true; then node a; fi",
		"exec node a.js":           "exec node a.js",
		"cd app":                   "cd app",
		"export A=1":               "export A=1",
		`node "unbalanced`:         `node "unbalanced`,
		"A=\"x y\" node a.js":      "A=\"x y\" node a.js",
		"a && && b":                "a && && b",
		"":                         "",
		"A=1":                      "A=1",
	} {
		if got := ShellStart(in); got != want {
			t.Errorf("ShellStart(%q) = %q, want %q", in, got, want)
		}
	}
}
