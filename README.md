# lambda-gorunner

Lightweight and fast lambda function for SSH commands execution written in `Go`.

It will try to connect and execute list of given ssh commands on `EC2` instances in current AWS account without stopping on errors.
The `stdout` outputs will be stored and returned in the resulting response.

The intended use case for this tool is to collect facts from ssh commands execution on large scale of instances. Lamdas are cheap, but there is a duration limit for them (up to 15 minutes), so function should be fast and be able to spawn multiple SSH sessions in parallel.
Sessions number is controlled by `MAX_SESSIONS` variable. Ssh session connection timeouts are controlled by `TIMEOUT` variable.

Lambda function deploy is provided by [serverless](https://serverless.com/) framework.

## Setup

- Install [golang](https://golang.org/doc/install) for building lambda function

- Install [serverless](https://serverless.com/) framework

      npm install -g serverless

- Setup variables in `.env`

      cp env.sample .env

## Build

    make build

## Run locally

    make local

## Deploy

    make deploy

## Remove

    make remove

## Configuration

### Dotenv

All variables from `.env` will be loaded into serverless environment.
No additional plugins are needed.

### Commands

You could provide list of commands to run on remote instances by setting `FACTS` variable. The `FACTS` is a `json` string: `{<label1>: <command1>, <label2>: <command2>}`.

    export FACTS='{"kernel": "uname -rs", "host": "hostname"}'

### Multi-connection

Use `MAX_SESSIONS` to increase number of parallel commands execution:

    export MAX_SESSIONS=1024

### SSH Authentication

You need to provide openssh key to connect to EC2 instances

- `SSH_KEY_PATH` - path to the unencrypted openssh key
- `SSH_KEY` - string with the key itself

And you could set `USERS` to provide a comma separated list of ssh users to use for login:

    USERS=ec2-user,centos

## TODO

- Add EC2 filters
- Speedup:
  - Most slowdowns are the ssh connections `EOF` errors which freeze goroutines queue
    - timeouts are not working for them
    - try using parallel `.Dial()` calls and apply timeouts and abort with first answer
  - try to avoid OS throttling using batches of ssh sessions with timeouts between them
- Tests
