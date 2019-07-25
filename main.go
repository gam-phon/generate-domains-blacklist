package main

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Structs
type blacklist struct {
	IgnoreCount      int
	WhitelistedCount int
	Data             []string
}

type source map[string]*blacklist

type name struct {
	URL   string
	Value string
}

type timeRestriction struct {
	Pass   map[string]string
	Ignore []string
}
type dataTemp struct {
	TimeRestrictions timeRestriction
	Sources          map[string]*blacklist
}

func getBlacklistURLs(fileName string) ([]url.URL, error) {
	file, err := os.Open(fileName)
	if err != nil {
		log.Println("Error: readFile", err)
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	urls := []url.URL{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[:1] == "#" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			log.Println("Error: Parse", err)
			return nil, err
		}
		urls = append(urls, *u)
	}
	return urls, nil
}

func parseBlacklist(url string, r io.Reader, names chan<- name, trust bool) error {
	rx_comment := regexp.MustCompile(`^(#|$)`)
	rx_inline_comment := regexp.MustCompile(`\s*#\s*[a-z0-9-].*$`)
	rx_u := regexp.MustCompile(
		`^@*\|\|([a-z0-9.-]+[.][a-z]{2,})\^?(\$(popup|third-party))?$`)
	rx_l := regexp.MustCompile(`^([a-z0-9.-]+[.][a-z]{2,})$`)
	rx_h := regexp.MustCompile(
		`^[0-9]{1,3}[.][0-9]{1,3}[.][0-9]{1,3}[.][0-9]{1,3}\s+([a-z0-9.-]+[.][a-z]{2,})$`)
	rx_mdl := regexp.MustCompile(`^"[^"]+","([a-z0-9.-]+[.][a-z]{2,})",`)
	rx_b := regexp.MustCompile(`^([a-z0-9.-]+[.][a-z]{2,}),.+,[0-9: /-]+,`)
	rx_dq := regexp.MustCompile(`^address=/([a-z0-9.-]+[.][a-z]{2,})/.`)
	rx_trusted := regexp.MustCompile(`^([*a-z0-9.-]+)\s*(@\S+)?$`)
	// Trusted
	// time_restrictions = {}

	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)
	rx_set := []*regexp.Regexp{rx_u, rx_l, rx_h, rx_mdl, rx_b, rx_dq}
	rx_setTrust := []*regexp.Regexp{rx_trusted}
	for scanner.Scan() {
		line := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if rx_comment.MatchString(line) {
			continue
		}
		line = rx_inline_comment.ReplaceAllLiteralString(line, "")
		if trust {
			for _, rx := range rx_setTrust {
				if !rx.MatchString(line) {
					continue
				}
				res := rx.FindStringSubmatch(line)
				names <- name{URL: url, Value: res[1]}
			}
		} else {
			for _, rx := range rx_set {
				if !rx.MatchString(line) {
					continue
				}
				res := rx.FindStringSubmatch(line)
				names <- name{URL: url, Value: res[1]}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Println("Error: parseList", err)
		return err
	}
	return nil
}

func parseLocalList(fileName string) (map[string]struct{}, map[string]string, error) {
	file, err := os.Open(fileName)
	if err != nil {
		log.Println("Error: readFile", err)
		return nil, nil, err
	}
	defer file.Close()

	rx_comment := regexp.MustCompile(`^(#|$)`)
	rx_inline_comment := regexp.MustCompile(`\s*#\s*[a-z0-9-].*$`)
	rx_trusted := regexp.MustCompile(`^([*a-z0-9.-]+)\s*(@\S+)?$`)

	// https://emersion.fr/blog/2017/sets-in-go/
	namesSet := make(map[string]struct{})
	timeRestriction := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	rx_setTrust := []*regexp.Regexp{rx_trusted}
	for scanner.Scan() {
		line := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if rx_comment.MatchString(line) {
			continue
		}
		line = rx_inline_comment.ReplaceAllLiteralString(line, "")
		for _, rx := range rx_setTrust {
			if !rx.MatchString(line) {
				continue
			}
			res := rx.FindStringSubmatch(line)
			namesSet[res[1]] = struct{}{}
			if res[2] != "" {
				timeRestriction[res[1]] = res[2]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Println("Error: whitelistNames", err)
		return nil, nil, err
	}
	return namesSet, timeRestriction, nil
}

// https://stackoverflow.com/questions/34464146/the-idiomatic-way-to-implement-generators-yield-in-golang-for-recursive-functi
func callURL(urls []url.URL) <-chan name {
	var wg sync.WaitGroup

	names := make(chan name)

	go func() {
		for _, u := range urls {
			wg.Add(1)
			go func(u url.URL) {
				defer wg.Done()
				if u.Scheme == "file" {
					file, err := os.Open(u.String()[5:])
					if err != nil {
						log.Println("Error: readFile", err)
						return
					}
					defer file.Close()
					parseBlacklist(u.String(), file, names, true)
				} else if u.Scheme == "https" || u.Scheme == "http" {
					req, err := http.NewRequest(http.MethodGet, u.String(), nil)
					if err != nil {
						log.Println("Error: NewRequest", err)
						return
					}
					body, err := send(req)
					if err != nil {
						log.Println("Error: Send", err)
						return
					}
					defer body.Close()
					parseBlacklist(u.String(), body, names, false)
				} else {
					log.Println("Error: Scheme not supported", u.Scheme)
				}
			}(u)
		}
		wg.Wait()
		close(names)
	}()

	return names

}

func hasSuffix(namesSet map[string]struct{}, name string) bool {
	parts := strings.Split(name, ".")
	for range parts {
		parts = parts[1:]
		if _, ok := namesSet[strings.Join(parts, ".")]; ok {
			return true
		}
	}
	return false
}

func main() {
	start := time.Now()

	urls, err := getBlacklistURLs("domains-blacklist.conf")
	if err != nil {
		log.Println("Error: getBlacklistURLs", err)
		return
	}

	namesSet := make(map[string]struct{})
	whitelistsSet, _, err := parseLocalList("domains-whitelist.txt")
	if err != nil {
		log.Println("Error: parseLocalList", err)
		return
	}

	timeRestrictionSet, timeRestrictions, err := parseLocalList("domains-time-restricted.txt")
	if err != nil {
		log.Println("Error: parseLocalList", err)
		return
	}
	for url := range timeRestrictionSet {
		if _, ok := whitelistsSet[url]; !ok {
			whitelistsSet[url] = struct{}{}
		}
	}
	/*
		if time_restricted_url:
		time_restricted_content, _trusted = load_from_url(time_restricted_url)
		time_restricted_names, time_restrictions = parse_time_restricted_list(
			time_restricted_content)

		if time_restricted_names:
			print("########## Time-based blacklist ##########\n")
			for name in time_restricted_names:
				print_restricted_name(name, time_restrictions)

		# Time restricted names should be whitelisted, or they could be always blocked
		whitelisted_names |= time_restricted_names
	*/

	sources := make(source)
	tmpSources := make(source)
	for _, url := range urls {
		sources[url.String()] = &blacklist{}
		tmpSources[url.String()] = &blacklist{}
	}
	for name := range callURL(urls) {
		if _, ok := namesSet[name.Value]; ok {
			sources[name.URL].IgnoreCount++
			continue
		}
		if _, ok := whitelistsSet[name.Value]; ok {
			sources[name.URL].WhitelistedCount++
			continue
		}
		namesSet[name.Value] = struct{}{}
		tmpSources[name.URL].Data = append(tmpSources[name.URL].Data, name.Value)
	}

	// another loop for has_suffix Because maybe in other list suffix appear before main site when fetching and main site already in the list
	for url := range tmpSources {
		for _, name := range tmpSources[url].Data {
			if hasSuffix(namesSet, name) {
				sources[url].IgnoreCount++
				continue
			}
			if hasSuffix(whitelistsSet, name) {
				sources[url].WhitelistedCount++
				continue
			}
			sources[url].Data = append(sources[url].Data, name)
		}
	}

	// Sort by reverse domain
	for url := range sources {
		sort.Slice(sources[url].Data, func(i, j int) bool {
			iParts := strings.Split(sources[url].Data[i], ".")
			jParts := strings.Split(sources[url].Data[j], ".")
			for i := len(iParts)/2 - 1; i >= 0; i-- {
				opp := len(iParts) - 1 - i
				iParts[i], iParts[opp] = iParts[opp], iParts[i]
			}
			for i := len(jParts)/2 - 1; i >= 0; i-- {
				opp := len(jParts) - 1 - i
				jParts[i], jParts[opp] = jParts[opp], jParts[i]
			}
			return strings.Join(iParts, ".") < strings.Join(jParts, ".")
		})
	}

	var totalIgnoreCount, totalWhitelistedCount, totalData int
	for url := range sources {
		totalIgnoreCount += sources[url].IgnoreCount
		totalWhitelistedCount += sources[url].WhitelistedCount
		totalData += len(sources[url].Data)
	}
	log.Println("Total Data:", totalData)
	log.Println("Total Ignored duplicates:", totalIgnoreCount)
	log.Println("Total Ignored whitelisted :", totalWhitelistedCount)

	// Template
	tpl, err := template.New("blacklists").Parse(`{{ if or .TimeRestrictions.Pass .TimeRestrictions.Ignore }}########## Time-based blacklist ##########
{{ range $key, $value := .TimeRestrictions.Pass -}}
{{ $key }}    {{ $value }}
{{ end }}{{ range $key := .TimeRestrictions.Ignore -}}
# ignored: [{{ $key }}] was in the time-restricted list, but without a time restriction label
{{ end }}
{{ end }}{{ range $key, $value := .Sources -}}
########## Blacklist from {{ $key}} ##########
{{ if $value.IgnoreCount -}}
# Ignored duplicates: {{ $value.IgnoreCount }}
{{ end }}
{{- if $value.WhitelistedCount -}}
# Ignored entries due to the whitelist: {{ $value.WhitelistedCount }}
{{ end }}
{{ range $index, $name := $value.Data -}}
{{ $name }}
{{ end }}
{{ end }}`)
	if err != nil {
		log.Println("Error: template", err)
		return
	}
	f, err := os.Create("results.txt")
	if err != nil {
		log.Println("Error: Create file", err)
		return
	}
	defer f.Close()
	data := dataTemp{Sources: sources, TimeRestrictions: timeRestriction{Pass: make(map[string]string)}}
	for url := range timeRestrictionSet {
		if _, ok := timeRestrictions[url]; ok {
			data.TimeRestrictions.Pass[url] = timeRestrictions[url]
		} else {
			data.TimeRestrictions.Ignore = append(data.TimeRestrictions.Ignore, url)
		}
	}
	if err = tpl.Execute(f, data); err != nil {
		log.Println("Error: Execute", err)
		return
	}
	// https://www.joeshaw.org/dont-defer-close-on-writable-files/
	if err = f.Sync(); err != nil {
		log.Println("Error: Sync", err)
		return
	}
	elapsed := time.Since(start)
	log.Printf("It tooks %s", elapsed)
}
