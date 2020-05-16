# GoHRec :: HTTP Request Recorder written in Golang

![Docker Cloud Automated build](https://img.shields.io/docker/cloud/automated/frxyt/gohrec.svg)
![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/frxyt/gohrec.svg)
![Docker Pulls](https://img.shields.io/docker/pulls/frxyt/gohrec.svg)
![GitHub issues](https://img.shields.io/github/issues/frxyt/gohrec.svg)
![GitHub last commit](https://img.shields.io/github/last-commit/frxyt/gohrec.svg)

> GoHRec logs HTTP requests it receive as JSON files, with all their details (including headers and body) and is able to redo these saved requests.

* Docker Hub: https://hub.docker.com/r/frxyt/gohrec
* GitHub: https://github.com/frxyt/gohrec

## Docker Hub Image

**`frxyt/gohrec`**

## Build

* Binary: `go build -o main .`
* Docker: `docker build -t frxyt/gohrec:latest .`

## Usage

* `docker run --rm -p 8080:8080 -v $(pwd):/gohrec/log frxyt/gohrec:latest`
* `docker-compose up`

## Options

### `gohrec record`: record requests

* `--listen`
* `--only-path`
* `--except-path`

### `gohrec redo`: redo a saved request

* `--request`
* `--host`

## License

This project and images are published under the MIT License.

```
MIT License

Copyright (c) 2020 FEROX YT EIRL, www.ferox.yt <devops@ferox.yt>
Copyright (c) 2020 Jérémy WALTHER <jeremy.walther@golflima.net>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```