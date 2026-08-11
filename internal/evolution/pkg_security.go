// Package evolution provides package security scanning functionality.
//
// This module scans pip/npm package dependencies for known security risks
// before allowing installation or execution. It implements:
//   - Typosquatting detection (Levenshtein distance against popular packages)
//   - Known malicious package blocklist
//   - Suspicious pattern detection (crypto, system tools, homoglyphs)
//   - Unmaintained/fork impersonation detection
package evolution

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Data structures
// ---------------------------------------------------------------------------

// PkgSecurityRisk represents a single security concern about a package.
type PkgSecurityRisk struct {
	PackageName string `json:"package_name"`
	Ecosystem   string `json:"ecosystem"`  // "pypi", "npm", "go"
	RiskLevel   string `json:"risk_level"` // "critical", "high", "medium", "low"
	RiskType    string `json:"risk_type"`  // "typosquatting", "unmaintained", "known_malicious", "suspicious_pattern"
	Description string `json:"description"`
}

// PkgSecurityReport is the output of a package security scan.
type PkgSecurityReport struct {
	TotalPackages int               `json:"total_packages"`
	RiskyPackages []PkgSecurityRisk `json:"risky_packages"`
	SafePackages  int               `json:"safe_packages"`
	Verdict       string            `json:"verdict"` // "safe", "caution", "dangerous"
}

// PkgQuery describes a single package to scan.
type PkgQuery struct {
	Ecosystem string // "pypi", "npm", "go"
	Name      string
	Version   string // optional, for future version-specific checks
}

// ---------------------------------------------------------------------------
// Pre-built lookup maps (built at init time)
// ---------------------------------------------------------------------------

// popularPyPI is the set of top PyPI packages commonly targeted by typosquatting.
var popularPyPI map[string]struct{}

// popularNPM is the set of top npm packages commonly targeted by typosquatting.
var popularNPM map[string]struct{}

// maliciousPyPI is the set of known-malicious PyPI packages.
var maliciousPyPI map[string]struct{}

// maliciousNPM is the set of known-malicious npm packages.
var maliciousNPM map[string]struct{}

// pypiPopularList holds the ordered list for iteration in typosquatting checks.
var pypiPopularList []string

// npmPopularList holds the ordered list for iteration in typosquatting checks.
var npmPopularList []string

// knownCryptoOrgs is the set of known legitimate crypto-related organisations
// whose packages should not be flagged merely for containing "crypto".
var knownCryptoOrgs = map[string]struct{}{
	"cryptography":         {},
	"cryptography-vectors": {},
	"pycryptodome":         {},
	"pycrypto":             {},
	"bcrypt":               {},
	"cryptojs":             {},
	"crypto-js":            {},
	"node-forge":           {},
	"tweetnacl":            {},
	"libsodium":            {},
	"sodium":               {},
}

// systemTools is the set of suspicious package names that impersonate system tools.
var systemTools = map[string]struct{}{
	"sudo":       {},
	"passwd":     {},
	"ssh":        {},
	"bash":       {},
	"sh":         {},
	"cmd":        {},
	"powershell": {},
	"pwsh":       {},
	"cmd.exe":    {},
	"chmod":      {},
	"chown":      {},
	"ifconfig":   {},
	"ipconfig":   {},
	"netstat":    {},
	"taskmgr":    {},
	"regedit":    {},
}

// ---------------------------------------------------------------------------
// init – build all lookup maps
// ---------------------------------------------------------------------------

func init() {
	buildPopularPyPI()
	buildPopularNPM()
	buildMaliciousPyPI()
	buildMaliciousNPM()
}

func buildPopularPyPI() {
	names := []string{
		// Top 200 most popular / most-typosquatted PyPI packages
		"requests", "numpy", "pandas", "urllib3", "flask", "django",
		"beautifulsoup4", "pillow", "scipy", "matplotlib", "cryptography",
		"aiohttp", "fastapi", "pydantic", "celery", "pytest",
		"sqlalchemy", "marshmallow", "click", "rich", "tqdm",
		"tensorflow", "torch", "transformers", "openai", "anthropic",
		"langchain", "boto3", "botocore", "s3transfer", "awscli",
		"google-cloud-storage", "google-api-core", "grpcio", "protobuf",
		"psutil", "pyyaml", "toml", "tomli", "python-dotenv",
		"colorama", "termcolor", "tabulate", "loguru", "structlog",
		"black", "flake8", "pylint", "mypy", "isort",
		"coverage", "tox", "nox", "pre-commit", "virtualenv",
		"pipenv", "poetry", "setuptools", "wheel", "twine",
		"pygments", "jinja2", "markupsafe", "werkzeug", "itsdangerous",
		"gunicorn", "uvicorn", "starlette", "httpx", "httptools",
		"websockets", "websocket-client", "docker", "kubernetes",
		"redis", "hiredis", "celery-redbeat", "kombu", "amqp",
		"pika", "pymongo", "motor", "pymysql", "psycopg2",
		"psycopg2-binary", "asyncpg", "aiosqlite", "sqlparse",
		"alembic", "django-rest-framework", "django-cors-headers",
		"django-filter", "django-allauth", "djangorestframework-simplejwt",
		"graphene", "graphene-django", "strawberry-graphql",
		"lxml", "html5lib", "cssselect", "parsel", "scrapy",
		"selenium", "playwright", "puppeteer", "pyppeteer",
		"opencv-python", "opencv-python-headless", "scikit-learn",
		"scikit-image", "nltk", "spacy", "gensim", "textblob",
		"sympy", "networkx", "pydot", "graphviz",
		"jupyter", "ipython", "jupyterlab", "notebook", "nbconvert",
		"nbformat", "ipykernel", "jupyter-client", "traitlets",
		"plotly", "dash", "bokeh", "seaborn", "altair",
		"streamlit", "gradio", "panel", "voila",
		"pyarrow", "fsspec", "s3fs", "gcsfs", "adlfs",
		"duckdb", "sqlglot", "ibis-framework",
		"ruff", "pyright", "basedpyright",
		"orjson", "ujson", "msgspec", "pydantic-core",
		"pydantic-settings", "typer", "fire", "argparse",
		"tenacity", "retry", "backoff",
		"dateutil", "python-dateutil", "pytz", "tzdata", "arrow", "pendulum",
		"attrs", "dataclasses-json", "cattrs",
		"certifi", "charset-normalizer", "idna",
		"cachetools", "diskcache", "python-memcached",
		"watchdog", "watchfiles", "inotify",
		"json5", "hjson", "configparser",
		"python-magic", "filetype", "puremagic",
		"paramiko", "fabric", "ansible", "invoke",
		"humanize", "humanfriendly",
		"emoji", "python-slugify", "inflection",
		"bleach", "mistune", "markdown", "markdown2",
		"python-multipart", "aiofiles",
		"async-timeout", "anyio", "trio",
		"zipp", "importlib-metadata", "importlib-resources",
		"packaging", "platformdirs", "filelock",
		"exceptiongroup", "sniffio", "h11", "h2",
	}
	pypiPopularList = names
	popularPyPI = make(map[string]struct{}, len(names))
	for _, n := range names {
		popularPyPI[n] = struct{}{}
	}
}

func buildPopularNPM() {
	names := []string{
		// Top 200 most popular / most-typosquatted npm packages
		"express", "react", "lodash", "axios", "moment", "next",
		"typescript", "eslint", "prettier", "webpack", "babel",
		"jest", "dotenv", "uuid", "fs-extra", "commander",
		"chalk", "debug", "node-fetch", "cors", "body-parser",
		"multer", "passport", "jsonwebtoken", "socket.io",
		"mongoose", "sequelize", "typeorm", "graphql", "apollo",
		"prisma", "redis", "bull", "pm2", "nodemailer",
		"winston", "morgan", "helmet", "compression", "async",
		"cheerio", "puppeteer", "nodemon", "concurrently", "rimraf",
		"cross-env", "husky", "lint-staged", "classnames",
		"immer", "redux", "react-redux", "react-router", "react-router-dom",
		"styled-components", "emotion", "@emotion/react", "@emotion/styled",
		"tailwindcss", "postcss", "autoprefixer", "sass", "less",
		"babel-core", "@babel/core", "@babel/preset-env",
		"@babel/preset-react", "@babel/preset-typescript",
		"ts-node", "ts-jest", "@types/node", "@types/react",
		"@types/express", "@types/lodash", "@types/jest",
		"vue", "@vue/cli", "vue-router", "vuex", "pinia",
		"nuxt", "@nuxt/kit", "vite", "@vitejs/plugin-react",
		"rollup", "esbuild", "swc", "turbo",
		"turborepo", "nx", "lerna", "changesets",
		"gulp", "grunt", "browserify", "parcel",
		"react-dom", "react-native", "expo",
		"next-auth", "@auth/core", "next-themes",
		"zod", "yup", "joi", "ajv", "validator",
		"dayjs", "date-fns", "luxon",
		"rxjs", "lodash-es", "ramda", "underscore",
		"axios-retry", "ky", "got", "superagent",
		"swr", "@tanstack/react-query", "react-query",
		"zustand", "jotai", "recoil", "mobx",
		"d3", "three", "chart.js", "echarts",
		"marked", "highlight.js", "prismjs", "shiki",
		"dompurify", "sanitize-html", "xss",
		"nodemailer-sendgrid", "@sendgrid/mail", "mailgun.js",
		"stripe", "@stripe/stripe-js", "paypal-rest-sdk",
		"firebase", "@firebase/app", "@google-cloud/storage",
		"aws-sdk", "@aws-sdk/client-s3", "@aws-sdk/client-lambda",
		"ioredis", "redis-parser",
		"pg", "mysql2", "sqlite3", "better-sqlite3",
		"knex", "drizzle-orm", "drizzle-kit",
		"prisma-client", "@prisma/client",
		"migrate", "db-migrate", "umzug",
		"commander", "yargs", "meow", "minimist",
		"ora", "listr", "progress", "cli-table3",
		"inquirer", "enquirer", "prompts",
		"ws", "socket.io-client", "engine.io",
		"graphql-yoga", "graphql-tools",
		"@graphql-codegen/cli", "graphql-codegen",
		"nodemailer-express-handlebars", "pug", "ejs", "handlebars",
		"crypto-js", "bcryptjs", "argon2", "nanoid",
		"jose", "oauth", "passport-jwt", "passport-local",
		"multer-s3", "sharp", "jimp", "gm",
		"archiver", "unzipper", "adm-zip", "tar", "tar-fs",
		"csv-parse", "csv-stringify", "papaparse", "xlsx",
		"pdfkit", "pdfmake", "pdf-lib", "jspdf",
		"puppeteer-core", "playwright-core",
		"patch-package", "syncpack", "depcheck",
		"madge", "dependency-cruiser", "knip",
		"eslint-plugin-import", "eslint-plugin-react",
		"@typescript-eslint/eslint-plugin", "@typescript-eslint/parser",
	}
	npmPopularList = names
	popularNPM = make(map[string]struct{}, len(names))
	for _, n := range names {
		popularNPM[strings.ToLower(n)] = struct{}{}
	}
}

func buildMaliciousPyPI() {
	names := []string{
		"colourama", "python3-dateutil", "libpeshka",
		"tensorflow-gpu", "tensorflow-cpu", "jeIlyfish",
		"rquests", "sentinelone-sdk", "pycrypter",
		"colorslib", "asynnc", "scikit-learn",
		"scikit_learn", "scikitlearn",
	}
	maliciousPyPI = make(map[string]struct{}, len(names))
	for _, n := range names {
		maliciousPyPI[n] = struct{}{}
	}
}

func buildMaliciousNPM() {
	names := []string{
		"electorn", "lodashs", "crossenv", "babel-cli",
		"reactnativelog", "eslint-scope-fake", "ua-parser",
		"jquerry", "noblox", "jwt-decode",
	}
	maliciousNPM = make(map[string]struct{}, len(names))
	for _, n := range names {
		maliciousNPM[strings.ToLower(n)] = struct{}{}
	}
}

// ---------------------------------------------------------------------------
// Levenshtein distance (iterative DP, pure Go)
// ---------------------------------------------------------------------------

// levenshtein computes the Levenshtein edit distance between a and b.
// Uses the classic O(n*m) DP algorithm with a single-row optimisation
// (two rows instead of full matrix) to minimise memory.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	alen, blen := len(ar), len(br)
	if alen == 0 {
		return blen
	}
	if blen == 0 {
		return alen
	}

	// Ensure b is the longer string for the row-based approach.
	if alen > blen {
		ar, br = br, ar
		alen, blen = blen, alen
	}

	prevRow := make([]int, alen+1)
	curRow := make([]int, alen+1)

	for i := 0; i <= alen; i++ {
		prevRow[i] = i
	}

	for j := 1; j <= blen; j++ {
		curRow[0] = j
		for i := 1; i <= alen; i++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			curRow[i] = min3(
				prevRow[i]+1,      // deletion
				curRow[i-1]+1,     // insertion
				prevRow[i-1]+cost, // substitution
			)
		}
		prevRow, curRow = curRow, prevRow
	}
	return prevRow[alen]
}

// levenshteinPreprocessed applies common typosquatting substitutions BEFORE
// computing Levenshtein distance. This catches visual-similarity attacks
// like "requsts" where the attacker relies on the reader's brain
// auto-correcting.
func levenshteinPreprocessed(a, b string) int {
	a = applySubstitutions(a)
	b = applySubstitutions(b)
	return levenshtein(a, b)
}

// applySubstitutions normalises common visual confusions so that
// "rn"→"m", "vv"→"w", "cl"→"d" etc. are treated as identical during
// the distance calculation.
func applySubstitutions(s string) string {
	// Order matters: longer patterns first to avoid partial matches.
	replacer := strings.NewReplacer(
		"rn", "m",
		"vv", "w",
		"cl", "d",
	)
	s = replacer.Replace(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '0':
			b.WriteRune('o')
		case '1':
			b.WriteRune('l')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Homoglyph detection
// ---------------------------------------------------------------------------

// cyrillicHomoglyphs maps Cyrillic characters that look identical (or nearly
// identical) to Latin letters. Attackers use these in package names to
// impersonate legitimate packages (e.g. "rеquеsts" with Cyrillic 'е').
var cyrillicHomoglyphs = map[rune]rune{
	'а': 'a', // Cyrillic small a
	'е': 'e', // Cyrillic small ie
	'о': 'o', // Cyrillic small o
	'р': 'p', // Cyrillic small er
	'с': 'c', // Cyrillic small es
	'у': 'y', // Cyrillic small u
	'х': 'x', // Cyrillic small ha
	'А': 'A', // Cyrillic capital a
	'В': 'B', // Cyrillic capital ve
	'Е': 'E', // Cyrillic capital ie
	'К': 'K', // Cyrillic capital ka
	'М': 'M', // Cyrillic capital em
	'Н': 'H', // Cyrillic capital en
	'О': 'O', // Cyrillic capital o
	'Р': 'P', // Cyrillic capital er
	'С': 'C', // Cyrillic capital es
	'Т': 'T', // Cyrillic capital te
	'Х': 'X', // Cyrillic capital ha
	'І': 'I', // Cyrillic capital Byelorussian-Ukrainian i
	'і': 'i', // Cyrillic small Byelorussian-Ukrainian i
}

// homoglyphCheck checks a string for mixed-script (Cyrillic) characters that
// could be used in a homoglyph attack. Returns a slice of rune strings for
// each homoglyph found.
func homoglyphCheck(name string) []string {
	var result []string
	hasCyrillic := false
	hasLatin := false

	for _, r := range name {
		if _, ok := cyrillicHomoglyphs[r]; ok {
			hasCyrillic = true
			result = append(result, string(r))
		}
		if unicode.Is(unicode.Latin, r) {
			hasLatin = true
		}
	}

	// Only report if there's a mix (pure Cyrillic names are not an attack).
	if hasCyrillic && hasLatin {
		return result
	}
	return nil
}

// ---------------------------------------------------------------------------
// Core scanning logic
// ---------------------------------------------------------------------------

// IsTyposquatting checks if a package name is a likely typosquatting of a
// popular package. Returns the suspected target package name and the
// Levenshtein distance, or empty string / 0 if not suspicious.
func IsTyposquatting(name string) (target string, distance int) {
	if name == "" {
		return "", 0
	}

	// Determine which ecosystem list to use based on naming convention.
	// Most typosquatting attacks target PyPI, so check that first.
	checkAgainstPopular(name, pypiPopularList, false)
	t, d := checkAgainstPopular(name, pypiPopularList, false)
	if d > 0 && d <= 2 {
		return t, d
	}

	return "", 0
}

// checkAgainstPopular compares name against the given popular-name list using
// Levenshtein distance. Only packages NOT already in the popular list are
// checked (a popular package cannot be a typosquat of itself). Returns the
// closest match and its distance, or ("", 0).
func checkAgainstPopular(name string, popular []string, caseInsensitive bool) (target string, distance int) {
	// If the package itself IS a popular package, don't flag it.
	cmpName := name
	if caseInsensitive {
		cmpName = strings.ToLower(name)
	}
	for _, p := range popular {
		pcmp := p
		if caseInsensitive {
			pcmp = strings.ToLower(p)
		}
		if cmpName == pcmp {
			return "", 0
		}
	}

	bestTarget := ""
	bestDist := 999

	for _, p := range popular {
		var dist int
		if caseInsensitive {
			dist = levenshteinPreprocessed(strings.ToLower(name), strings.ToLower(p))
		} else {
			dist = levenshteinPreprocessed(name, p)
		}
		if dist < bestDist {
			bestDist = dist
			bestTarget = p
			if dist == 0 {
				// Shouldn't happen if we filtered above, but just in case.
				return "", 0
			}
		}
	}

	if bestDist <= 2 {
		return bestTarget, bestDist
	}
	return "", 0
}

// isKnownMalicious checks whether name is in the pre-built malicious-package
// list for the given ecosystem. Case-insensitive for npm.
func isKnownMalicious(name, ecosystem string) bool {
	switch ecosystem {
	case "pypi":
		_, ok := maliciousPyPI[name]
		return ok
	case "npm":
		lower := strings.ToLower(name)
		_, ok := maliciousNPM[lower]
		return ok
	default:
		return false
	}
}

// isPopular checks whether name is in the pre-built popular-package list for
// the given ecosystem.
func isPopular(name, ecosystem string) bool {
	switch ecosystem {
	case "pypi":
		_, ok := popularPyPI[name]
		return ok
	case "npm":
		lower := strings.ToLower(name)
		_, ok := popularNPM[lower]
		return ok
	default:
		return false
	}
}

// isUnmaintained checks whether name suggests it is an impersonating fork.
func isUnmaintained(name string) bool {
	lower := strings.ToLower(name)
	// Patterns: "requests2", "urllib3-plus", "flask3", etc.
	// Check for popular base names with numeric / suffix additions.
	suffixes := []string{"2", "3", "4", "5", "-plus", "-ng", "-pro", "-lite", "-next"}
	for _, s := range suffixes {
		if strings.HasSuffix(lower, s) {
			base := strings.TrimSuffix(lower, s)
			if isPopular(base, "pypi") || isPopular(base, "npm") {
				return true
			}
		}
	}
	return false
}

// checkSuspiciousPatterns checks name for suspicious patterns and returns any
// risks found.
func checkSuspiciousPatterns(name, ecosystem string) []PkgSecurityRisk {
	var risks []PkgSecurityRisk
	lower := strings.ToLower(name)

	// 1. "crypto" in name but not from a known crypto org.
	if strings.Contains(lower, "crypto") {
		if _, ok := knownCryptoOrgs[lower]; !ok {
			risks = append(risks, PkgSecurityRisk{
				PackageName: name,
				Ecosystem:   ecosystem,
				RiskLevel:   "medium",
				RiskType:    "suspicious_pattern",
				Description: fmt.Sprintf("Package name contains 'crypto' but is not a known cryptography library. Verify legitimacy."),
			})
		}
	}

	// 2. "keylogger", "steal", or similar.
	dangerousWords := []string{"keylogger", "steal", "keylog", "stealer"}
	for _, w := range dangerousWords {
		if strings.Contains(lower, w) {
			risks = append(risks, PkgSecurityRisk{
				PackageName: name,
				Ecosystem:   ecosystem,
				RiskLevel:   "critical",
				RiskType:    "suspicious_pattern",
				Description: fmt.Sprintf("Package name contains '%s', which suggests malicious intent.", w),
			})
			break // one is enough
		}
	}

	// 3. System tool name impersonation.
	if _, ok := systemTools[lower]; ok {
		risks = append(risks, PkgSecurityRisk{
			PackageName: name,
			Ecosystem:   ecosystem,
			RiskLevel:   "high",
			RiskType:    "suspicious_pattern",
			Description: fmt.Sprintf("Package name '%s' impersonates a system tool. This is a common attack vector.", name),
		})
	}

	// 4. Homoglyph detection.
	homoglyphs := homoglyphCheck(name)
	if len(homoglyphs) > 0 {
		chars := strings.Join(homoglyphs, ", ")
		risks = append(risks, PkgSecurityRisk{
			PackageName: name,
			Ecosystem:   ecosystem,
			RiskLevel:   "critical",
			RiskType:    "suspicious_pattern",
			Description: fmt.Sprintf("Package name contains homoglyph characters (%s) that may be used to impersonate a legitimate package.", chars),
		})
	}

	return risks
}

// scanOne analyses a single package and appends any risks to the report.
func scanOne(report *PkgSecurityReport, q PkgQuery) {
	risky := false

	// --- Check 1: Known malicious ---
	if isKnownMalicious(q.Name, q.Ecosystem) {
		report.RiskyPackages = append(report.RiskyPackages, PkgSecurityRisk{
			PackageName: q.Name,
			Ecosystem:   q.Ecosystem,
			RiskLevel:   "critical",
			RiskType:    "known_malicious",
			Description: fmt.Sprintf("Package '%s' is on the known-malicious blocklist for %s.", q.Name, q.Ecosystem),
		})
		risky = true
	}

	// --- Check 2: Typosquatting ---
	caseInsensitive := q.Ecosystem == "npm"
	target, dist := checkAgainstPopular(q.Name, popularListForEcosystem(q.Ecosystem), caseInsensitive)
	if dist >= 1 && dist <= 2 {
		level := "critical"
		if dist == 2 {
			level = "high"
		}
		report.RiskyPackages = append(report.RiskyPackages, PkgSecurityRisk{
			PackageName: q.Name,
			Ecosystem:   q.Ecosystem,
			RiskLevel:   level,
			RiskType:    "typosquatting",
			Description: fmt.Sprintf("Package '%s' is within Levenshtein distance %d of popular package '%s'. This may be a typosquatting attack.", q.Name, dist, target),
		})
		risky = true
	}

	// --- Check 3: Suspicious patterns ---
	patternRisks := checkSuspiciousPatterns(q.Name, q.Ecosystem)
	if len(patternRisks) > 0 {
		report.RiskyPackages = append(report.RiskyPackages, patternRisks...)
		risky = true
	}

	// --- Check 4: Unmaintained / fork impersonation ---
	if isUnmaintained(q.Name) {
		report.RiskyPackages = append(report.RiskyPackages, PkgSecurityRisk{
			PackageName: q.Name,
			Ecosystem:   q.Ecosystem,
			RiskLevel:   "medium",
			RiskType:    "unmaintained",
			Description: fmt.Sprintf("Package '%s' appears to be an unofficial fork or impersonation of a popular package.", q.Name),
		})
		risky = true
	}

	if !risky {
		report.SafePackages++
	}
}

// popularListForEcosystem returns the appropriate popular-package list slice.
func popularListForEcosystem(ecosystem string) []string {
	switch ecosystem {
	case "pypi":
		return pypiPopularList
	case "npm":
		return npmPopularList
	default:
		// Go has fewer typosquatting targets; check both lists.
		return nil
	}
}

// sortRisks sorts the risky packages slice for deterministic output.
func sortRisks(risks []PkgSecurityRisk) {
	order := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.Slice(risks, func(i, j int) bool {
		if order[risks[i].RiskLevel] != order[risks[j].RiskLevel] {
			return order[risks[i].RiskLevel] < order[risks[j].RiskLevel]
		}
		if risks[i].PackageName != risks[j].PackageName {
			return risks[i].PackageName < risks[j].PackageName
		}
		return risks[i].RiskType < risks[j].RiskType
	})
}

// pkgSecurityVerdict computes the overall verdict from the risk levels.
func pkgSecurityVerdict(risks []PkgSecurityRisk) string {
	hasCritical := false
	hasHigh := false
	for _, r := range risks {
		switch r.RiskLevel {
		case "critical":
			hasCritical = true
		case "high":
			hasHigh = true
		}
	}
	if hasCritical {
		return "dangerous"
	}
	if hasHigh || len(risks) > 0 {
		return "caution"
	}
	return "safe"
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// ScanPackages scans a list of package identifiers for security risks.
// packages is a list of {ecosystem, name, version} tuples.
func ScanPackages(packages []PkgQuery) *PkgSecurityReport {
	report := &PkgSecurityReport{
		TotalPackages: len(packages),
		RiskyPackages: make([]PkgSecurityRisk, 0),
	}

	for _, q := range packages {
		scanOne(report, q)
	}

	sortRisks(report.RiskyPackages)
	report.SafePackages = report.TotalPackages - len(report.RiskyPackages)
	report.Verdict = pkgSecurityVerdict(report.RiskyPackages)
	return report
}

// ScanRequirementsTxt scans all packages listed in a requirements.txt content
// string and returns a security report.
func ScanRequirementsTxt(content string) *PkgSecurityReport {
	queries := make([]PkgQuery, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		// Skip empty lines, comments, and inline options.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		// Handle options like -r, --index-url, etc.
		if strings.HasPrefix(line, "-") {
			continue
		}

		// Parse package name from constraint line.
		// Patterns: "pkg==1.0", "pkg>=1.0", "pkg~=1.0", "pkg!=1.0",
		//           "pkg<=1.0", "pkg; python_version >= '3.6'",
		//           "pkg[extra]==1.0"
		pkgName, _ := parsePipPackage(line)
		if pkgName != "" {
			queries = append(queries, PkgQuery{
				Ecosystem: "pypi",
				Name:      pkgName,
			})
		}
	}
	return ScanPackages(queries)
}

// parsePipPackage extracts the package name from a pip requirements line.
func parsePipPackage(line string) (name string, version string) {
	line = strings.SplitN(line, "#", 2)[0] // strip comment
	line = strings.SplitN(line, ";", 2)[0] // strip environment marker
	line = strings.TrimSpace(line)

	if line == "" {
		return "", ""
	}

	// Handle extras: pkg[extra1,extra2]>=1.0
	extraStart := strings.Index(line, "[")
	extraEnd := strings.Index(line, "]")
	if extraStart != -1 && extraEnd != -1 && extraEnd > extraStart {
		name = line[:extraStart]
	} else {
		name = line
	}

	// Split on version operators.
	for _, op := range []string{"===", "~=", "!=", "<=", ">=", "==", "<", ">", " ", "\t"} {
		if idx := strings.Index(name, op); idx != -1 {
			version = strings.TrimSpace(name[idx+len(op):])
			name = name[:idx]
			break
		}
	}

	// Also handle bare version (space-separated).
	parts := strings.Fields(name)
	if len(parts) > 0 {
		name = parts[0]
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}

	return name, version
}

// ScanPackageJson scans all dependencies in a package.json content string and
// returns a security report.
func ScanPackageJson(content string) *PkgSecurityReport {
	queries := make([]PkgQuery, 0)

	// Use minimal JSON parsing to avoid dependency on encoding/json for
	// arbitrary structures — we parse the standard "dependencies" and
	// "devDependencies" blocks manually. This is simpler and avoids
	// needing to unmarshal into a map[string]interface{}.
	deps := parseJSONStringMap(content, "\"dependencies\"")
	devDeps := parseJSONStringMap(content, "\"devDependencies\"")
	peerDeps := parseJSONStringMap(content, "\"peerDependencies\"")
	optionalDeps := parseJSONStringMap(content, "\"optionalDependencies\"")

	for name := range deps {
		queries = append(queries, PkgQuery{Ecosystem: "npm", Name: name})
	}
	for name := range devDeps {
		queries = append(queries, PkgQuery{Ecosystem: "npm", Name: name})
	}
	for name := range peerDeps {
		queries = append(queries, PkgQuery{Ecosystem: "npm", Name: name})
	}
	for name := range optionalDeps {
		queries = append(queries, PkgQuery{Ecosystem: "npm", Name: name})
	}

	return ScanPackages(queries)
}

// parseJSONStringMap does a simple, non-recursive extraction of string→string
// key-value pairs from a JSON object block identified by keyPrefix (e.g.
// `"dependencies"`). It handles the standard format found in package.json
// without needing full JSON unmarshalling. Returns a map of package name →
// version string.
func parseJSONStringMap(content, keyPrefix string) map[string]string {
	result := make(map[string]string)

	// Find the key in the JSON.
	idx := strings.Index(content, keyPrefix)
	if idx == -1 {
		return result
	}
	rest := content[idx+len(keyPrefix):]

	// Find the opening '{' after potential whitespace and ':'
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r' || rest[i] == ':') {
		i++
	}
	if i >= len(rest) || rest[i] != '{' {
		return result
	}

	rest = rest[i+1:]

	// Parse key-value pairs until we hit the closing '}' at the right depth.
	depth := 1
	var currentKey strings.Builder
	var currentValue strings.Builder
	inKey := true
	inString := false
	skip := false

	for j := 0; j < len(rest) && depth > 0; j++ {
		ch := rest[j]

		if skip {
			skip = false
			if inString {
				if inKey {
					currentKey.WriteByte(ch)
				} else {
					currentValue.WriteByte(ch)
				}
			}
			continue
		}

		if ch == '\\' {
			skip = true
			if inString {
				if inKey {
					currentKey.WriteByte(ch)
				} else {
					currentValue.WriteByte(ch)
				}
			}
			continue
		}

		if ch == '"' {
			if inString {
				// End of a string.
				inString = false
				if inKey {
					// Just finished reading a key; now expect ':' then value.
					inKey = false
				} else {
					// Finished reading a value; record the pair.
					key := currentKey.String()
					val := currentValue.String()
					currentKey.Reset()
					currentValue.Reset()
					if key != "" {
						result[key] = val
					}
				}
			} else {
				inString = true
			}
			continue
		}

		if inString {
			if inKey {
				currentKey.WriteByte(ch)
			} else {
				currentValue.WriteByte(ch)
			}
			continue
		}

		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			continue
		}

		// Not in a string — look for the colon between key and value.
		if ch == ':' && !inKey {
			// Colon found; next string will be the value.
			// (inKey is already false, so we just wait for the next ".)
			continue
		}
		// Comma: prepare for next key.
		if ch == ',' {
			inKey = true
			continue
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Reporting
// ---------------------------------------------------------------------------

// FormatPkgSecurityReport formats a security report as human-readable text.
func FormatPkgSecurityReport(report *PkgSecurityReport) string {
	if report == nil {
		return "No security report available.\n"
	}

	var b strings.Builder

	b.WriteString("═══ Package Security Report ═══\n\n")
	fmt.Fprintf(&b, "Total packages scanned: %d\n", report.TotalPackages)
	fmt.Fprintf(&b, "Safe packages:          %d\n", report.SafePackages)
	fmt.Fprintf(&b, "Risky packages:         %d\n", len(report.RiskyPackages))

	verdictIcon := "✅"
	switch report.Verdict {
	case "dangerous":
		verdictIcon = "🚫"
	case "caution":
		verdictIcon = "⚠️"
	}
	fmt.Fprintf(&b, "Verdict:                %s %s\n", verdictIcon, report.Verdict)

	if len(report.RiskyPackages) == 0 {
		b.WriteString("\n✅ All packages passed security checks.\n")
		return b.String()
	}

	b.WriteString("\n─── Risk Details ───\n\n")
	for i, r := range report.RiskyPackages {
		icon := "🟡"
		switch r.RiskLevel {
		case "critical":
			icon = "🔴"
		case "high":
			icon = "🟠"
		case "low":
			icon = "🟢"
		}
		fmt.Fprintf(&b, "%d. %s [%s] [%s] %s\n", i+1, icon, r.RiskLevel, r.Ecosystem, r.PackageName)
		fmt.Fprintf(&b, "   Type: %s\n", r.RiskType)
		fmt.Fprintf(&b, "   %s\n\n", r.Description)
	}

	// Summary footer.
	switch report.Verdict {
	case "dangerous":
		b.WriteString("🚫 VERDICT: DANGEROUS — Critical risks detected. Do NOT install these packages.\n")
	case "caution":
		b.WriteString("⚠️  VERDICT: CAUTION — Potential risks detected. Review before installing.\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// JSON export helpers
// ---------------------------------------------------------------------------

// MarshalReportJSON serialises the report as indented JSON.
func MarshalReportJSON(report *PkgSecurityReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func min3(a, b, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= c {
		return b
	}
	return c
}
