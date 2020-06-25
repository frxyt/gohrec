// Copyright (c) 2020 FEROX YT EIRL, www.ferox.yt <devops@ferox.yt>
// Copyright (c) 2020 Jérémy WALTHER <jeremy.walther@golflima.net>
// See <https://github.com/frxyt/gohrec> for details.

package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/http/pprof"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const redactedString = "**REDACTED**"

type redactFlag struct {
	regex   regexp.Regexp
	replace string
}

func (rf *redactFlag) Redact(text string) string {
	return rf.regex.ReplaceAllString(text, rf.replace)
}

func (rf *redactFlag) Set(value string) error {
	index := strings.LastIndex(value, "/")
	var pattern string
	if index > -1 {
		pattern = value[:index]
		rf.replace = value[index+1:]
	} else {
		pattern = value
		rf.replace = redactedString
	}
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	rf.regex = *regex
	return nil
}

func (rf *redactFlag) String() string {
	if str := fmt.Sprintf("%s/%s", rf.regex.String(), rf.replace); str != "/" {
		return str
	}
	return "regex[/replacement]"
}

type arrayRedactFlag []redactFlag

func (arf *arrayRedactFlag) Redact(text string) string {
	for _, item := range *arf {
		text = item.Redact(text)
	}
	return text
}

func (arf *arrayRedactFlag) Set(value string) error {
	item := redactFlag{}
	if err := item.Set(value); err != nil {
		return err
	}
	*arf = append(*arf, item)
	return nil
}

func (arf *arrayRedactFlag) String() string {
	if arf == nil {
		return "[]"
	}
	out := []string{}
	for _, item := range *arf {
		out = append(out, "`"+item.String()+"`")
	}
	return "[ " + strings.Join(out, ", ") + " ]"
}

type goHRec struct {
	listen, dateFormat          string
	onlyPath, exceptPath        *regexp.Regexp
	redactBody, redactHeaders   arrayRedactFlag
	maxBodySize                 int64
	targetURL                   *url.URL
	echo, index, proxy, verbose bool
	indexLogger                 *log.Logger
}

type recordingTime struct {
	requestReceived, requestForwarded, responseReceived, responseSent time.Time
}

type baseInfo struct {
	ID                          string
	Date, DateUTC               time.Time
	DateUnixNano                int64
	Protocol                    string
	Headers                     []string
	ContentLength               int64
	Body                        string
	Trailers, TransferEncodings []string
}

type requestInfo struct {
	RemoteAddr         string
	Host, Method, Path string
	Query              []string
	URI                string
}

type responseInfo struct {
	Status     string
	StatusCode int
	Compressed bool
}

type requestRecord struct {
	baseInfo
	requestInfo
}

type responseRecord struct {
	baseInfo
	responseInfo
}

func dumpValues(in map[string][]string) []string {
	out := []string{}
	for name, values := range in {
		for _, value := range values {
			out = append(out, fmt.Sprintf("%s: %s", name, value))
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

func (ghr goHRec) redactRecord(record *baseInfo) {
	if record == nil {
		return
	}

	if ghr.redactHeaders != nil && record.Headers != nil && len(record.Headers) > 0 {
		for i := 0; i < len(record.Headers); i++ {
			record.Headers[i] = ghr.redactHeaders.Redact(record.Headers[i])
		}
	}

	if ghr.redactHeaders != nil && record.Trailers != nil && len(record.Trailers) > 0 {
		for i := 0; i < len(record.Trailers); i++ {
			record.Trailers[i] = ghr.redactHeaders.Redact(record.Trailers[i])
		}
	}

	if ghr.redactBody != nil {
		record.Body = ghr.redactBody.Redact(record.Body)
	}
}

func (ghr goHRec) saveJSON(json []byte, id string, received time.Time, suffix string, req string) (string, error) {
	filebase := fmt.Sprintf("%s", received.Format(ghr.dateFormat))
	filepath := filebase
	if i := strings.LastIndex(filepath, "/"); i > -1 {
		filepath = filebase[:i]
	}
	if err := os.MkdirAll(filepath, 0755); err != nil {
		ghr.log("Error while preparing save: %s", err)
		return filepath, err
	}
	filename := fmt.Sprintf("%s%09d.%s.%s.json", filebase, received.Nanosecond(), id, suffix)

	if err := ioutil.WriteFile(filename, json, 0644); err != nil {
		ghr.log("Error while saving: %s", err)
		return filename, err
	}

	if ghr.index {
		ghr.indexLogger.Printf("%s\t%s\t%s", id, filename, req)
	}

	return filename, nil
}

func (ghr goHRec) saveRequest(req string, record requestRecord, rt recordingTime) {
	ghr.redactRecord(&record.baseInfo)

	if record.ID == "" {
		record.ID = makeRequestID(req, rt.requestReceived)
	}

	json, err := json.MarshalIndent(record, "", " ")
	if err != nil {
		ghr.log("Error while serializing record: %s", err)
		return
	}

	filename, err := ghr.saveJSON(json, record.ID, rt.requestReceived, "request", req)

	ghr.log("Recorded: %s (%s)",
		filename,
		req,
	)
}

func makeRequestName(r *http.Request) string {
	return fmt.Sprintf("[%s] %s http://%s%s", r.RemoteAddr, r.Method, r.Host, r.RequestURI)
}

func makeRequestID(req string, received time.Time) string {
	unixHash := make([]byte, 8)
	binary.BigEndian.PutUint64(unixHash, uint64(received.UnixNano()))
	randHash := make([]byte, 4)
	binary.BigEndian.PutUint32(randHash, rand.Uint32())
	md5Hash := md5.Sum([]byte(req))
	return base64.RawURLEncoding.EncodeToString(append(append(unixHash[:], randHash[:]...), md5Hash[:]...))
}

func (ghr goHRec) isNotWhitelisted(r *http.Request, req string) bool {
	if ghr.onlyPath != nil && !ghr.onlyPath.MatchString(r.URL.Path) {
		ghr.log("Skipped: doesn't match --only-path. (%s)", req)
		return true
	}
	return false
}

func (ghr goHRec) isBlacklisted(r *http.Request, req string) bool {
	if ghr.exceptPath != nil && ghr.exceptPath.MatchString(r.URL.Path) {
		ghr.log("Skipped: match --except-path. (%s)", req)
		return true
	}
	return false
}

func (ghr goHRec) prepareRequestRecord(r *http.Request, rt recordingTime) requestRecord {
	return requestRecord{
		baseInfo{
			Date:              rt.requestReceived,
			DateUTC:           rt.requestReceived.UTC(),
			DateUnixNano:      rt.requestReceived.UnixNano(),
			Protocol:          r.Proto,
			Headers:           dumpValues(r.Header),
			ContentLength:     r.ContentLength,
			Trailers:          dumpValues(r.Trailer),
			TransferEncodings: r.TransferEncoding,
		},
		requestInfo{
			RemoteAddr: r.RemoteAddr,
			Host:       r.Host,
			Method:     r.Method,
			Path:       r.URL.Path,
			Query:      dumpValues(r.URL.Query()),
			URI:        r.RequestURI,
		},
	}
}

func (ghr goHRec) handler(w http.ResponseWriter, r *http.Request) {
	rt := recordingTime{requestReceived: time.Now()}
	req := makeRequestName(r)

	if ghr.isNotWhitelisted(r, req) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Skipped: not whitelisted.")
		return
	}

	if ghr.isBlacklisted(r, req) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Skipped: blacklisted.")
		return
	}

	record := ghr.prepareRequestRecord(r, rt)

	var bodyReader io.Reader
	bodyReader = r.Body
	if ghr.maxBodySize != -1 {
		bodyReader = io.LimitReader(r.Body, ghr.maxBodySize)
	}
	body, err := ioutil.ReadAll(bodyReader)
	if err != nil {
		ghr.log("Error while dumping body: %s", err)
	}
	record.Body = fmt.Sprintf("%s", body)

	w.WriteHeader(http.StatusCreated)
	if ghr.echo {
		if json, err := json.MarshalIndent(record, "", " "); err == nil {
			fmt.Fprintf(w, "%s\n", json)
		}
	}
	fmt.Fprintln(w, "Recorded.")

	rt.responseSent = time.Now()
	defer ghr.saveRequest(req, record, rt)
}

func (ghr goHRec) saveResponse(req string, record responseRecord, rt recordingTime, body io.ReadCloser) {
	var bodyReader io.Reader
	bodyReader = body
	if ghr.maxBodySize != -1 {
		bodyReader = io.LimitReader(body, ghr.maxBodySize)
	}
	bodyContent, err := ioutil.ReadAll(bodyReader)
	if err != nil {
		ghr.log("Error while dumping body: %s", err)
	}
	record.Body = fmt.Sprintf("%s", bodyContent)

	ghr.redactRecord(&record.baseInfo)

	if record.ID == "" {
		record.ID = makeRequestID(req, rt.requestReceived)
	}

	json, err := json.MarshalIndent(record, "", " ")
	if err != nil {
		ghr.log("Error while serializing record: %s", err)
		return
	}

	filename, err := ghr.saveJSON(json, record.ID, rt.requestReceived, "response", req)
	ghr.log("Recorded: %s (%s)", filename, req)
}

func (ghr goHRec) proxyModifyResponse(r *http.Response) error {
	rt := recordingTime{responseReceived: time.Now()}
	req := makeRequestName(r.Request)

	rt.requestReceived = rt.responseReceived
	if reqRecHeader := r.Request.Header.Get("X-Gohrec-Request-Received"); reqRecHeader != "" {
		if reqRec, err := strconv.ParseInt(reqRecHeader, 10, 64); err == nil {
			rt.requestReceived = time.Unix(0, reqRec)
		}
	}

	reqid := r.Request.Header.Get("X-Gohrec-Request-Id")
	if reqid == "" {
		reqid = makeRequestID(req, rt.requestReceived)
		ghr.log("Cannot find X-Gohrec-Request-Id in response request, generating a new one: %s", reqid)
	}
	r.Header.Add("X-Gohrec-Response-Id", reqid)

	record := responseRecord{
		baseInfo{
			ID:                reqid,
			Date:              rt.responseReceived,
			DateUTC:           rt.responseReceived.UTC(),
			DateUnixNano:      rt.responseReceived.UnixNano(),
			Protocol:          r.Proto,
			Headers:           dumpValues(r.Header),
			ContentLength:     r.ContentLength,
			Trailers:          dumpValues(r.Trailer),
			TransferEncodings: r.TransferEncoding,
		},
		responseInfo{
			Compressed: !r.Uncompressed,
			Status:     r.Status,
			StatusCode: r.StatusCode,
		},
	}

	var body []byte
	var err error
	if r.Body != nil {
		body, err = ioutil.ReadAll(r.Body)
		if err != nil {
			ghr.log("Error while reading body: %s", err)
		}
	}
	r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

	rt.responseSent = time.Now()
	defer ghr.saveResponse(req, record, rt, ioutil.NopCloser(bytes.NewBuffer(body)))

	return nil
}

func (ghr goHRec) proxyHandler(w http.ResponseWriter, r *http.Request) {
	rt := recordingTime{requestReceived: time.Now()}
	req := makeRequestName(r)

	proxy := httputil.NewSingleHostReverseProxy(ghr.targetURL)

	if ghr.isNotWhitelisted(r, req) || ghr.isBlacklisted(r, req) {
		proxy.ServeHTTP(w, r)
		return
	}

	reqid := makeRequestID(req, rt.requestReceived)
	r.Header.Add("X-Gohrec-Request-Id", reqid)
	r.Header.Add("X-Gohrec-Request-Received", strconv.FormatInt(rt.requestReceived.UnixNano(), 10))

	record := ghr.prepareRequestRecord(r, rt)
	record.ID = reqid

	var body []byte
	var err error
	if r.Body != nil {
		body, err = ioutil.ReadAll(r.Body)
		if err != nil {
			ghr.log("Error while reading body: %s", err)
		}
	}
	r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

	proxy.ModifyResponse = ghr.proxyModifyResponse
	rt.requestForwarded = time.Now()
	proxy.ServeHTTP(w, r)

	var bodyReader io.Reader
	bodyReader = ioutil.NopCloser(bytes.NewBuffer(body))
	if ghr.maxBodySize != -1 {
		bodyReader = io.LimitReader(r.Body, ghr.maxBodySize)
	}
	bodyContent, err := ioutil.ReadAll(bodyReader)
	if err != nil {
		ghr.log("Error while dumping body: %s", err)
	}
	record.Body = fmt.Sprintf("%s", bodyContent)

	defer ghr.saveRequest(req, record, rt)
}

func record() {
	record := flag.NewFlagSet("record", flag.PanicOnError)
	listen := record.String("listen", ":8080", "Interface and port to listen.")
	dateFormat := record.String("date-format", "2006-01-02/15-04-05_", "Go format of the date used in record filenames, required subfolders are created automatically.")
	onlyPath := record.String("only-path", "", "If set, record only requests that match the specified URL path pattern.")
	exceptPath := record.String("except-path", "", "If set, record requests that don't match the specified URL path pattern.")
	maxBodySize := record.Int64("max-body-size", -1, "Maximum size of body in bytes that will be recorded, `-1` to disallow limit.")
	targetURL := record.String("target-url", "", "Target URL used when proxy mode is enabled.")
	echo := record.Bool("echo", false, "Echo logged request on calls.")
	index := record.Bool("index", false, "Build an index of hashes and their clear text representation.")
	proxy := record.Bool("proxy", false, "Enable proxy mode.")
	enablePprof := record.Bool("pprof", false, "Enable pprof endpoints /debug/pprof/*.")
	verbose := record.Bool("verbose", false, "Log processed request status.")

	var redactBody arrayRedactFlag
	var redactHeaders arrayRedactFlag
	record.Var(&redactBody, "redact-body", "If set, matching parts of the specified pattern in request body will be redacted. Can contain a specific replacement string after a `/`.")
	record.Var(&redactHeaders, "redact-headers", "If set, matching parts of the specified pattern in request headers will be redacted. Can contain a specific replacement string after a `/`.")

	record.Parse(os.Args[2:])

	makeRegexp := func(s *string) *regexp.Regexp {
		if s == nil || *s == "" {
			return nil
		}
		return regexp.MustCompile(*s)
	}

	makeURL := func(s *string) *url.URL {
		if s == nil || *s == "" {
			return nil
		}
		url, err := url.Parse(*targetURL)
		if err != nil {
			log.Fatal(err)
		}
		return url
	}

	gohrec := goHRec{
		listen:        *listen,
		dateFormat:    *dateFormat,
		onlyPath:      makeRegexp(onlyPath),
		exceptPath:    makeRegexp(exceptPath),
		maxBodySize:   *maxBodySize,
		redactBody:    redactBody,
		redactHeaders: redactHeaders,
		targetURL:     makeURL(targetURL),
		echo:          *echo,
		index:         *index,
		proxy:         *proxy,
		verbose:       *verbose,
	}

	if gohrec.index {
		if f, err := os.OpenFile("index.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err != nil {
			log.Fatalf("Error while creating index.log: %s", err)
		} else {
			gohrec.indexLogger = log.New(f, "", log.LUTC)
			defer f.Close()
		}
	}

	log.Printf("  listen: %s", gohrec.listen)
	log.Printf("  only-path: %s", gohrec.onlyPath)
	log.Printf("  except-path: %s", gohrec.exceptPath)
	log.Printf("  max-body-size: %d", gohrec.maxBodySize)
	log.Printf("  redact-body: %s", gohrec.redactBody.String())
	log.Printf("  redact-headers: %s", gohrec.redactHeaders.String())
	log.Printf("  date-format: %s", gohrec.dateFormat)
	log.Printf("  target-url: %s", gohrec.targetURL)
	log.Printf("  echo: %t", gohrec.echo)
	log.Printf("  index: %t", gohrec.index)
	log.Printf("  proxy: %t", gohrec.proxy)
	log.Printf("  pprof: %t", *enablePprof)
	log.Printf("  verbose: %t", gohrec.verbose)

	rand.Seed(time.Now().UnixNano())

	gohrecMux := http.NewServeMux()

	if gohrec.proxy {
		if gohrec.targetURL == nil {
			panic("--target-url is required when proxy mode is enabled!")
		}
		gohrecMux.HandleFunc("/", gohrec.proxyHandler)
	} else {
		gohrecMux.HandleFunc("/", gohrec.handler)
	}

	if *enablePprof {
		// Register pprof handlers
		gohrecMux.HandleFunc("/debug/pprof/", pprof.Index)
		gohrecMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		gohrecMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		gohrecMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		gohrecMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	log.Fatal(http.ListenAndServe(gohrec.listen, gohrecMux))
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
