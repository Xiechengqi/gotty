OUTPUT_DIR = ./builds
GIT_COMMIT = `git rev-parse HEAD | cut -c1-7`
VERSION = $(shell git describe --tags --always)
BUILD_OPTIONS = -ldflags "-X main.Version=$(VERSION)"

ifeq ($(DEV), 1)
	BUILD_OPTIONS += -tags dev
	WEBPACK_MODE = development
else
	WEBPACK_MODE = production
endif

export CGO_ENABLED=0

gotty: main.go assets server/*.go webtty/*.go backend/*.go Makefile
	go build ${BUILD_OPTIONS}

docker:
	docker build --build-arg VERSION=$(VERSION) . -t gotty-bash:$(VERSION)

.PHONY: all docker assets
assets: bindata/static/js/gotty.js.map \
	bindata/static/js/gotty.js \
	bindata/static/js/gotty.licenses.txt \
	bindata/static/js/gotty.js.LICENSE.txt \
	bindata/static/index.html \
	bindata/static/icon.svg \
	bindata/static/favicon.ico \
	bindata/static/css/index.css \
	bindata/static/css/xterm.css \
	bindata/static/css/xterm_customize.css \
	bindata/static/manifest.json \
	bindata/static/icon_192.png

all: gotty

bindata/static bindata/static/css bindata/static/js:
	mkdir -p $@

bindata/static/%: resources/% | bindata/static/css
	cp "$<" "$@"

bindata/static/css/%.css: resources/%.css | bindata/static
	cp "$<" "$@"

bindata/static/css/xterm.css: js/node_modules/@xterm/xterm/css/xterm.css | bindata/static
	cp "$<" "$@"

js/node_modules/@xterm/xterm/css/xterm.css:
	cd js && \
	npm ci

bindata/static/js/gotty.js.map bindata/static/js/gotty.js bindata/static/js/gotty.licenses.txt bindata/static/js/gotty.js.LICENSE.txt &: $(wildcard js/src/*.ts js/src/*.tsx js/src/*.scss) js/webpack.config.js js/tsconfig.json js/package-lock.json | js/node_modules/webpack
	cd js && \
	npx webpack --mode=$(WEBPACK_MODE)

js/node_modules/webpack: js/package.json js/package-lock.json
	cd js && \
	npm ci

README-options:
	./gotty --help | sed '1,/GLOBAL OPTIONS/ d' > options.txt.tmp
	sed -f README.md.sed -i README.md
	rm options.txt.tmp

tools:
	go install github.com/tcnksm/ghr@latest

test:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"
	go test ./...

cross_compile:
	./scripts/build-linux.sh

targz:
	mkdir -p ${OUTPUT_DIR}/dist
	cd ${OUTPUT_DIR}/pkg/; for osarch in *; do (cd $$osarch; tar zcvf ../../dist/gotty_${VERSION}_$$osarch.tar.gz ./*); done;

shasums:
	cd ${OUTPUT_DIR}/dist; sha256sum * > ./SHA256SUMS

release-artifacts: gotty cross_compile targz shasums

release:
	ghr -draft ${VERSION} ${OUTPUT_DIR}/dist # -c ${GIT_COMMIT} --delete --prerelease -u sorenisanerd -r gotty ${VERSION}

clean:
	rm -fr gotty builds js/dist bindata/static js/node_modules

addcontributors:
	-gh issue list -s all -L 1000 --json author -t "$$(/bin/echo -e '{{ range . }}{{ .author.login }}\n{{ end }}')" | sort | uniq | xargs -Ifoo -t all-contributors add foo bug --commitTemplate '<%= (newContributor ? "Add" : "Update") %> @<%= username %> as a contributor'
	-gh pr list -s all -L 1000 --json author -t "$$(/bin/echo -e '{{ range . }}{{ .author.login }}\n{{ end }}')" | sort | uniq | xargs -Ifoo -t all-contributors add foo code --commitTemplate '<%= (newContributor ? "Add" : "Update") %> @<%= username %> as a contributor'

.PHONY: test-js typecheck test-e2e
test-js:
	cd js && npm test

typecheck:
	cd js && npm run typecheck

test-e2e:
	cd js && npm run test:e2e
