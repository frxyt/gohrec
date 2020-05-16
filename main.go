// Copyright (c) 2020 FEROX YT EIRL, www.ferox.yt <devops@ferox.yt>
// Copyright (c) 2020 Jérémy WALTHER <jeremy.walther@golflima.net>
// See <https://github.com/frxyt/gohrec> for details.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// GoHRec records HTTP requests.
type GoHRec struct {
	listen               string
	onlyPath, exceptPath *regexp.Regexp
}

// ResponseRecord contains a logged HTTP request.
type ResponseRecord struct {
	Date, DateUTC                time.Time
	DateUnixNano                 int64
	RemoteAddr                   string
	Host, Method, Path, Protocol string
	Query                        []string
	URI                          string
	Headers                      []string
	ContentLength                int64
	Body                         *string
}

func dumpValues(in map[string][]string) []string {
	out := []string{}
	for name, values := range in {
		for _, value := range values {
			out = append(out, fmt.Sprintf("%v: %v", name, value))
		}
	}
	sort.Strings(out)
	return out
}

func (ghr GoHRec) handler(w http.ResponseWriter, r *http.Request) {
	date := time.Now()

	log := func(format string, a ...interface{}) {
		s := fmt.Sprintf(format, a...)
		fmt.Fprint(w, s)
		log.Print(s)
	}

	req := fmt.Sprintf("%s %s%s", r.Method, r.Host, r.URL.Path)

	if ghr.onlyPath != nil && !ghr.onlyPath.MatchString(r.URL.Path) {
		w.WriteHeader(http.StatusOK)
		log("Skipped: doesn't match --only-path. (%s)", req)
		return
	}

	if ghr.exceptPath != nil && ghr.exceptPath.MatchString(r.URL.Path) {
		w.WriteHeader(http.StatusOK)
		log("Skipped: match --except-path. (%s)", req)
		return
	}

	record := ResponseRecord{
		Date:          date,
		DateUTC:       date.UTC(),
		DateUnixNano:  date.UnixNano(),
		RemoteAddr:    r.RemoteAddr,
		Host:          r.Host,
		Method:        r.Method,
		Path:          r.URL.Path,
		Protocol:      r.Proto,
		Query:         dumpValues(r.URL.Query()),
		URI:           r.RequestURI,
		Headers:       dumpValues(r.Header),
		ContentLength: r.ContentLength,
	}

	if r.ContentLength > 0 {
		body, err := ioutil.ReadAll(r.Body)
		if err == nil {
			*record.Body = fmt.Sprintf("%s", body)
		} else {
			log("Error while dumping body: %s", err)
		}
	}

	json, err := json.MarshalIndent(record, "", " ")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log("Error while serializing record: %s", err)
		return
	}
	filepath := fmt.Sprintf("%s/%s/%s", date.Format("2006-01-02"), strings.Split(r.Host, ":")[0], r.URL.Path)
	filepath = path.Clean(filepath)
	err = os.MkdirAll(filepath, 0755)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log("Error while preparing save: %s", err)
		return
	}
	filename := fmt.Sprintf("%s/%s-%d.json", filepath, date.Format("15-04-05"), date.Nanosecond())
	err = ioutil.WriteFile(filename, json, 0644)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log("Error while saving: %s", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	log("Recorded: %s (%d µs)", filename, time.Now().Sub(date).Microseconds())
}

func record() {
	record := flag.NewFlagSet("record", flag.PanicOnError)
	listen := record.String("listen", ":8080", "Interface and port to listen.")
	onlyPath := record.String("only-path", "", "If set, record only requests that match the specified URL path pattern.")
	exceptPath := record.String("except-path", "", "If set, record requests that don't match the specified URL path pattern.")
	record.Parse(os.Args[2:])

	makeRegexp := func(s *string) *regexp.Regexp {
		if s == nil || *s == "" {
			return nil
		}
		return regexp.MustCompile(*s)
	}

	gohrec := GoHRec{
		listen:     *listen,
		onlyPath:   makeRegexp(onlyPath),
		exceptPath: makeRegexp(exceptPath),
	}

	log.Printf("  listen: %s", gohrec.listen)
	log.Printf("  only-path: %s", gohrec.onlyPath)
	log.Printf("  except-path: %s", gohrec.exceptPath)

	http.HandleFunc("/", gohrec.handler)
	log.Fatal(http.ListenAndServe(gohrec.listen, nil))
}

func redo() {
	redo := flag.NewFlagSet("redo", flag.PanicOnError)
	request := redo.String("request", "", "JSON file of the request to redo.")
	host := redo.String("host", "", "If set, change the host of the request to the one specified here.")
	redo.Parse(os.Args[2:])

	log.Printf("  request: %s", *request)
	log.Printf("  host: %s", *host)
}

func main() {
	log.Print("[frxyt/gohrec] <https://github.com/frxyt/gohrec>")

	if len(os.Args) < 2 {
		log.Fatal("Expected `record` or `redo` subcommands.")
	}

	switch os.Args[1] {
	case "record":
		record()
	case "redo":
		redo()
	default:
		log.Fatal("Expected `record` or `redo` subcommands.")
	}
}
