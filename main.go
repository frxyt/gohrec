// Copyright (c) 2020 FEROX YT EIRL, www.ferox.yt <devops@ferox.yt>
// Copyright (c) 2020 Jérémy WALTHER <jeremy.walther@golflima.net>
// See <https://github.com/frxyt/gohrec> for details.

package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type goHRec struct {
	listen, dateFormat   string
	onlyPath, exceptPath *regexp.Regexp
	echo, index, verbose bool
}

type responseRecord struct {
	Date, DateUTC                time.Time
	DateUnixNano                 int64
	RemoteAddr                   string
	Host, Method, Path, Protocol string
	Query                        []string
	URI                          string
	Headers                      []string
	ContentLength                int64
	Body                         string
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

func (ghr goHRec) handler(w http.ResponseWriter, r *http.Request) {
	date := time.Now()

	log := func(format string, a ...interface{}) {
		s := fmt.Sprintf(format, a...)
		fmt.Fprint(w, s+"\n")
		if ghr.verbose {
			log.Print(s)
		}
	}

	req := fmt.Sprintf("[%s] %s %s", r.Host, r.Method, r.URL.Path)

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

	record := responseRecord{
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
		if err != nil {
			log("Error while dumping body: %s", err)
		}
		record.Body = fmt.Sprintf("%s", body)
	}

	json, err := json.MarshalIndent(record, "", " ")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log("Error while serializing record: %s", err)
		return
	}
	filepath := fmt.Sprintf("%s", date.Format(ghr.dateFormat))
	err = os.MkdirAll(filepath, 0755)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log("Error while preparing save: %s", err)
		return
	}
	md5Hash := md5.Sum([]byte(req))
	md5String := hex.EncodeToString(md5Hash[:])
	filename := fmt.Sprintf("%s%09d_%s.json", filepath, date.Nanosecond(), md5String)
	err = ioutil.WriteFile(filename, json, 0644)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log("Error while saving: %s", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	if ghr.echo {
		fmt.Fprintf(w, "%s\n", json)
	}
	log("Recorded: %s (%d µs)", filename, time.Now().Sub(date).Microseconds())

	if ghr.index {
		err = os.MkdirAll("index", 0755)
		if err == nil {
			if _, err := os.Stat(fmt.Sprintf("index/%s", md5String)); os.IsNotExist(err) {
				if err := ioutil.WriteFile(fmt.Sprintf("index/%s", md5String), []byte(req), 0644); err != nil {
					log("Error while creating index: %s", err)
				}
			}
		} else {
			log("Error while creating `index` directory: %s", err)
		}
	}
}

func record() {
	record := flag.NewFlagSet("record", flag.PanicOnError)
	listen := record.String("listen", ":8080", "Interface and port to listen.")
	dateFormat := record.String("date-format", "2006-01-02/15-04-05_", "Go format of the date used in record filenames, required subfolders are created automatically.")
	onlyPath := record.String("only-path", "", "If set, record only requests that match the specified URL path pattern.")
	exceptPath := record.String("except-path", "", "If set, record requests that don't match the specified URL path pattern.")
	echo := record.Bool("echo", false, "Echo logged request on calls.")
	index := record.Bool("index", false, "Build an index of hashes and their clear text representation.")
	verbose := record.Bool("verbose", false, "Log processed request status.")
	record.Parse(os.Args[2:])

	makeRegexp := func(s *string) *regexp.Regexp {
		if s == nil || *s == "" {
			return nil
		}
		return regexp.MustCompile(*s)
	}

	gohrec := goHRec{
		listen:     *listen,
		dateFormat: *dateFormat,
		onlyPath:   makeRegexp(onlyPath),
		exceptPath: makeRegexp(exceptPath),
		echo:       *echo,
		index:      *index,
		verbose:    *verbose,
	}

	log.Printf("  listen: %s", gohrec.listen)
	log.Printf("  only-path: %s", gohrec.onlyPath)
	log.Printf("  except-path: %s", gohrec.exceptPath)
	log.Printf("  date-format: %s", gohrec.dateFormat)
	log.Printf("  echo: %t", gohrec.echo)
	log.Printf("  index: %t", gohrec.index)
	log.Printf("  verbose: %t", gohrec.verbose)

	http.HandleFunc("/", gohrec.handler)
	log.Fatal(http.ListenAndServe(gohrec.listen, nil))
}

func redo() {
	redo := flag.NewFlagSet("redo", flag.PanicOnError)
	request := redo.String("request", "", "JSON file of the request to redo.")
	host := redo.String("host", "", "If set, change the host of the request to the one specified here.")
	url := redo.String("url", "", "If set, change the URL of the request to the one specified here.")
	verbose := redo.Bool("verbose", false, "Display request dump too.")
	redo.Parse(os.Args[2:])

	log.Printf("  request: %s", *request)
	log.Printf("  host: %s", *host)
	log.Printf("  url: %s", *url)
	log.Printf("  verbose: %t", *verbose)

	content, err := ioutil.ReadFile(*request)
	if err != nil {
		log.Fatalf("Error while reading request file: %s", err)
	}

	type responseRecord struct {
		Body, Host, Method, URI string
		Headers                 []string
	}

	var record responseRecord
	err = json.Unmarshal(content, &record)
	if err != nil {
		log.Fatalf("Error while unmarshalling request file: %s", err)
	}

	if *host != "" {
		record.Host = *host
	}

	if *url != "" {
		record.URI = *url
	}

	req, err := http.NewRequest(record.Method, record.URI, bytes.NewBufferString(record.Body))
	if err != nil {
		log.Fatalf("Error while preparing request: %s", err)
	}
	for _, header := range record.Headers {
		split := strings.SplitN(header, ": ", 2)
		req.Header.Add(split[0], split[1])
	}

	if *verbose {
		dump, err := httputil.DumpRequestOut(req, true)
		if err != nil {
			log.Fatalf("Error while dumping prepared request: %s", err)
		}
		log.Printf("Request:\n%s\n", dump)
	}

	timeout, err := time.ParseDuration("60s")
	client := http.Client{
		Timeout: timeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error while sending request: %s", err)
	}
	defer resp.Body.Close()
	dump, err := httputil.DumpResponse(resp, true)
	if err != nil {
		log.Fatalf("Error while dumping response: %s", err)
	}
	log.Printf("Response:\n%s\n", dump)
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
