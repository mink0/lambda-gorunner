.PHONY: build clean deploy gomodgen remove

env_file:=.env

ifneq ("$(wildcard $(env_file))","")
	include $(env_file)
	export
endif

gomodgen:
	chmod u+x gomod.sh
	./gomod.sh

build: gomodgen
	export GO111MODULE=on
	env GOOS=linux go build -ldflags="-s -w" -o bin/gorunner gorunner/*.go

clean:
	rm -rf ./bin ./vendor Gopkg.lock

deploy: clean build
	sls deploy --verbose

local: clean build
	sls invoke local -f gorunner

invoke:
	sls invoke

remove:
	sls remove
