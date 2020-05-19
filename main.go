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
	listen, dateFormat, redactString                string
	onlyPath, exceptPath, redactBody, redactHeaders *regexp.Regexp
	echo, index, verbose                            bool
}

type recordingTime struct {
	received, responded, deferred, saved time.Time
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

func (ghr goHRec) log(format string, a ...interface{}) {
	if ghr.verbose {
		log.Printf(format, a...)
	}
}

func (ghr goHRec) save(req string, record responseRecord, rt recordingTime) {
	rt.deferred = time.Now()

	if ghr.redactHeaders != nil && record.Headers != nil && len(record.Headers) > 0 {
		for i := 0; i < len(record.Headers); i++ {
			record.Headers[i] = ghr.redactHeaders.ReplaceAllString(record.Headers[i], ghr.redactString)
		}
	}

	if ghr.redactBody != nil {
		record.Body = ghr.redactBody.ReplaceAllString(record.Body, ghr.redactString)
	}

	json, err := json.MarshalIndent(record, "", " ")
	if err != nil {
		ghr.log("Error while serializing record: %s", err)
		return
	}

	filebase := fmt.Sprintf("%s", rt.received.Format(ghr.dateFormat))
	filepath := filebase
	if i := strings.LastIndex(filepath, "/"); i > -1 {
		filepath = filebase[:i]
	}
	if err = os.MkdirAll(filepath, 0755); err != nil {
		ghr.log("Error while preparing save: %s", err)
		return
	}
	md5Hash := md5.Sum([]byte(req))
	md5String := hex.EncodeToString(md5Hash[:])
	filename := fmt.Sprintf("%s%09d_%s.json", filebase, rt.received.Nanosecond(), md5String)

	if err = ioutil.WriteFile(filename, json, 0644); err != nil {
		ghr.log("Error while saving: %s", err)
		return
	}

	if ghr.index {
		if err = os.MkdirAll("index", 0755); err == nil {
			if _, err := os.Stat(fmt.Sprintf("index/%s", md5String)); os.IsNotExist(err) {
				if err := ioutil.WriteFile(fmt.Sprintf("index/%s", md5String), []byte(req), 0644); err != nil {
					ghr.log("Error while creating index: %s", err)
				}
			}
		} else {
			ghr.log("Error while creating `index` directory: %s", err)
		}
	}

	rt.saved = time.Now()
	ghr.log("Recorded: %s (%s) (responded: %d µs, saved: %d µs)",
		filename,
		req,
		rt.responded.Sub(rt.received).Microseconds(),
		rt.saved.Sub(rt.deferred).Microseconds(),
	)
}

func (ghr goHRec) handler(w http.ResponseWriter, r *http.Request) {
	rt := recordingTime{received: time.Now()}
	req := fmt.Sprintf("[%s] %s %s", r.Host, r.Method, r.URL.Path)

	if ghr.onlyPath != nil && !ghr.onlyPath.MatchString(r.URL.Path) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Skipped: not whitelisted.")
		ghr.log("Skipped: doesn't match --only-path. (%s)", req)
		return
	}

	if ghr.exceptPath != nil && ghr.exceptPath.MatchString(r.URL.Path) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Skipped: blacklisted.")
		ghr.log("Skipped: match --except-path. (%s)", req)
		return
	}

	record := responseRecord{
		Date:          rt.received,
		DateUTC:       rt.received.UTC(),
		DateUnixNano:  rt.received.UnixNano(),
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
			ghr.log("Error while dumping body: %s", err)
			dump, err := httputil.DumpRequest(r, true)
			if err != nil {
				log.Fatalf("Error while dumping response: %s", err)
			}
			log.Printf("%s\n", dump)
		}
		record.Body = fmt.Sprintf("%s", body)
	}

	w.WriteHeader(http.StatusCreated)
	if ghr.echo {
		if json, err := json.MarshalIndent(record, "", " "); err == nil {
			fmt.Fprintf(w, "%s\n", json)
		}
	}
	fmt.Fprintf(w, "Recorded: %d µs.\n", time.Now().Sub(rt.received).Microseconds())

	rt.responded = time.Now()
	defer ghr.save(req, record, rt)
}

func record() {
	record := flag.NewFlagSet("record", flag.PanicOnError)
	listen := record.String("listen", ":8080", "Interface and port to listen.")
	dateFormat := record.String("date-format", "2006-01-02/15-04-05_", "Go format of the date used in record filenames, required subfolders are created automatically.")
	onlyPath := record.String("only-path", "", "If set, record only requests that match the specified URL path pattern.")
	exceptPath := record.String("except-path", "", "If set, record requests that don't match the specified URL path pattern.")
	redactBody := record.String("redact-body", "", "If set, matching parts of the specified pattern in request body will be redacted.")
	redactHeaders := record.String("redact-headers", "", "If set, matching parts of the specified pattern in request headers will be redacted.")
	redactString := record.String("redact-string", "**REDACTED**", "Replacement string for redacted content.")
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
		listen:        *listen,
		dateFormat:    *dateFormat,
		onlyPath:      makeRegexp(onlyPath),
		exceptPath:    makeRegexp(exceptPath),
		redactBody:    makeRegexp(redactBody),
		redactHeaders: makeRegexp(redactHeaders),
		redactString:  *redactString,
		echo:          *echo,
		index:         *index,
		verbose:       *verbose,
	}

	log.Printf("  listen: %s", gohrec.listen)
	log.Printf("  only-path: %s", gohrec.onlyPath)
	log.Printf("  except-path: %s", gohrec.exceptPath)
	log.Printf("  redact-body: %s", gohrec.redactBody)
	log.Printf("  redact-headers: %s", gohrec.redactHeaders)
	log.Printf("  redact-string: %s", gohrec.redactString)
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
	timeout := redo.String("timeout", "60s", "Timeout of the request to redo.")
	url := redo.String("url", "", "If set, change the URL of the request to the one specified here.")
	verbose := redo.Bool("verbose", false, "Display request dump too.")
	redo.Parse(os.Args[2:])

	log.Printf("  request: %s", *request)
	log.Printf("  host: %s", *host)
	log.Printf("  timeout: %s", *timeout)
	log.Printf("  url: %s", *url)
	log.Printf("  verbose: %t", *verbose)

	reqtout, err := time.ParseDuration(*timeout)
	if err != nil {
		log.Fatalf("Error while parsing timeout: %s", err)
	}

	content, err := ioutil.ReadFile(*request)
	if err != nil {
		log.Fatalf("Error while reading request file: %s", err)
	}

	type responseRecord struct {
		Body, Host, Method, URI string
		Headers                 []string
	}

	var record responseRecord
	if err = json.Unmarshal(content, &record); err != nil {
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

	client := http.Client{
		Timeout: reqtout,
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
