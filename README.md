# Artemis

Artemis is a command-line tool for automated testing of REST APIs, built with Go and Cobra. It provides a convenient way to ensure the stability and correctness of your API endpoints through automated testing procedures.

## Features

- Easy-to-use command-line interface (CLI) powered by Cobra
- Supports testing of REST API endpoints
- Customizable testing scenarios and assertions
- Integration with continuous integration (CI) pipelines
- Detailed test reports and logs
- Interactive CLI that prompts users for input during execution

## Installation

To install Artemis, make sure you have Go installed and then run:

```bash
source ./install.sh
```

## Commands

### Command for testing YAML file

```sh
artemis test -f sample.yaml
```

A run prints a line per step as it finishes, the checks that did not pass under
the step that made them, and a summary. It exits 0 only if everything passed:

```
scenario: checkout (checkout.yaml)
  ok    login                         2ms
  FAIL  orders                       <1ms
         $.status equals ok, got pending
  ERROR fetch order                  <1ms
         error performing request: Get "http://127.0.0.1:1/orders": connection refused

Scenarios     1  (1 errored)
Steps         3  (1 passed, 1 failed, 1 errored)
Assertions    3  (2 passed, 1 failed)
FAIL in 8ms
```

### Command to convert Postman collection to YAML format

An additional feature that i shipped with this is to convert postman collection format to artemis yaml format for faster configuration

```sh
artemis generate -f postman_collection.json
```
**Note**: After generating a YAML file from a Postman collection, manual adjustments might be necessary to tailor the YAML file according to specific requirements. The generated file serves as a starting point and helps in maintaining the structure and format consistent with the original collection.

### Logging

The terminal output above is all a run writes by default: no log file is created
unless you ask for one. Pass `-l` or `--log=` to also write a detailed JSON log of
the run -- every request, every response and every error -- to that path:

```sh
artemis test -f sample.yaml -l custom_log_file.log
```

The log is appended to, so a path that already exists keeps its earlier runs. A
path that cannot be opened fails the command.

### Environment variables

Artemis supports loading environment variables from a specified `.env` file. This allows you to store sensitive information or configuration-specific details outside of your main configuration files.

For custom env file path:

```sh
artemis test -f sample.yaml -l custom_log_file.log -e dev.env
```
When running Artemis with the -e flag followed by the path to your environment file, Artemis will load the environment variables from that file and make them available during the execution of your tests. A file named with `-e` that cannot be loaded is warned about; the default `.env` is optional and its absence is silent.

**Remember not to commit your environment files to version control systems like Git, as they may contain sensitive information.**

# Configuration

## Basic YAML Config

This configuration defines a basic API request to generate a token. It includes the following parameters:

- **name**: Name of script
- **variables**: Variables that you want to use in the script
  - **name**: Name of the variable
  - **value**: Value of the variable
- **steps**
  - **name**: Name of the Step.
  - **type**: Type of the step (currently only supports REST APIs with json payloads)
  - **request**
    - **url**: The URL endpoint for the API request, with a placeholder "{{url}}" that will be replaced with the base URL defined in the configuration section.
    - **method**: HTTP method for the request (e.g., POST).
    - **headers**: Headers to be included in the request, specifying the Content-Type as "application/json".
    - **body**: JSON payload containing the username and password for authentication.
  - **response**:
    - **status_code**:

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
      request:
        url: "{{url}}/token"
        method: "POST"
        headers:
          Content-Type: "application/json"
        body: '{"username":"user_name","password":"password"}'
      response:
        status_code: 200
```

## Adding Variables

This configuration extends the basic API request by adding support for capturing variables from the response. In this case, it captures the access token from the response body and saves it as a variable named "token".

- **variables**: Defines a list of variables to capture from the response.
    - **name**: Name of the variable.
    - **path**: [JSON path](https://support.smartbear.com/alertsite/docs/monitors/api/endpoint/jsonpath.html) to locate the variable value in the response.

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
      request:
        url: "{{url}}/token"
        method: "POST"
        headers:
          Content-Type: "application/json"
        body: '{"username":"user_name","password":"password"}'
      response:
        status_code: 200
    scripts:
      - key: "token"
        path: "$.data.token.access_token"

```

## Placeholders

Anywhere a step's `url`, `body` or a header value is written, `{{name}}` is
replaced with the value of `name`. A name resolves against the scenario's
`variables:` and against anything an earlier step captured with `scripts:`.
Surrounding spaces are ignored, so `{{ url }}` and `{{url}}` are the same.

A captured value does not have to be a string. A number renders as it was
written (`42`, not `42.000000`), a boolean as `true` or `false`, a null as
`null`, and an object or array as compact JSON -- so a captured object can be
templated straight into a body:

```yaml
body: '{"user": {{user}}}'
```

Two things are errors, and fail the step before any request goes out:

- **An unknown name.** `{{tokn}}` is not quietly sent to the server as literal
  braces; the step fails saying which variable is missing.
- **An unclosed placeholder.** `{{token` with no `}}`, or `{{url}` with one
  closing brace, fails naming the placeholder and its position.

Substitution is single pass: a value that itself contains `{{x}}` is used as it
is and not expanded again. There is no escape syntax -- `{{` always opens a
placeholder, so a body that needs literal braces should keep them in a
variable's value. To use an environment variable, reference it from a variable's
value (`{{env.NAME}}`, below) rather than in a step.

## Adding Assertions

This configuration further enhances the API request by adding assertions to validate the response. It checks if the HTTP status code is 200.

- **response**: Specifies assertions to be performed on the response.
  - **status_code**: Expected HTTP status code.
  - **body**: Expected values in response.
    - **path**: The [JSON path](https://support.smartbear.com/alertsite/docs/monitors/api/endpoint/jsonpath.html) in the response body.
    - **operator**: The comparison to make. Defaults to `equals`.
    - **value**: Expected value. It keeps the type you write: `value: 200` is a number, `value: "200"` a string, `value: true` a boolean.
    - **type**: Optional. The JSON type the value at **path** must have -- one of `string`, `number`, `boolean`, `object`, `array`, `null`. A value of another type fails the check.

### Operators

| Operator | Checks |
| --- | --- |
| `equals` (default) | The value equals **value**. Numbers compare as numbers whichever way they are written, and arrays and objects compare element by element. |
| `contains` | A string contains **value** as a substring, an array has it as an element, or an object has it as a key. |
| `matches` | The value, rendered as a string, matches the regular expression in **value**. Unanchored, so `"ok"` matches `"not ok"`. |
| `exists` | The path resolves to a non-null value. `value: false` asserts the opposite -- the key must be absent. |
| `type` | The value's JSON type is **value** -- `string`, `number`, `boolean`, `object`, `array` or `null`. |
| `gt`, `gte`, `lt`, `lte` | The value is a number greater than, at least, less than or at most **value**. |

A check that cannot be made at all -- a malformed path, a path that is not in the
response, an operator Artemis does not know, a regular expression that will not
compile, `gt` against an object -- is reported as an errored assertion with the
reason, and fails the run.

```yaml
    response:
      status_code: 200
      body:
        - path: "$.data.message"
          value: "success"
        - path: "$.data.id"
          operator: gt
          value: 0
        - path: "$.data.token"
          operator: exists
        - path: "$.data.roles"
          operator: contains
          value: "admin"
        - path: "$.data.email"
          operator: matches
          value: ".+@.+"
        - path: "$.data.count"
          type: "number"
          value: 3
```

```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
      request:
        url: "{{url}}/token"
        method: "POST"
        headers:
          Content-Type: "application/json"
        body: '{"username":"user_name","password":"password"}'
      response:
        status_code: 200
        body: 
          - path: "$.data.message"
            value: "success"
            type: "string"
```

## Meta Section

This section introduces additional metadata for configuring advanced features such as multiple calls of the same API, specifying the maximum number of calls, polling intervals, and exit conditions.

- **retry**: How many times a step may be attempted, and how long to wait between attempts.
  - **times**: the total number of attempts, not the number of retries after the first — `times: 3` sends at most three requests. Omitted, zero or negative means one attempt; a step is never attempted zero times.
  - **delay**: a duration string (`"500ms"`, `"2s"`, `"1m30s"`) slept *between* attempts — never before the first, never after the last. Omitted, there is no wait at all.

  Retrying stops as soon as an attempt passes: the status code matched and every assertion held. The older scalar form `retry: 5` still works and means `times: 5`.
```yaml
name: "API Collection"
variables:
  - name: "url"
    value: "https://api.example.com/v2"
type: functional
steps:
  - name: "Login"
    type: api
      request:
        url: "{{url}}/token"
        method: "POST"
        headers:
          Content-Type: "application/json"
        body: '{"username":"user_name","password":"password"}'
      response:
        status_code: 200
        body: 
          - path: "$.data.message"
            value: "success"
            type: "string"
      retry:
        times: 5
        delay: "2s"
```
## Environment support

```yaml
configuration:
  url: "{{env.url}}"
  secret: "{{env.secret}}"
```

Here, `{{env.url}}` and `{{env.secret}}` are placeholders that will be replaced with the actual values of the url and secret environment variables shown below, when the YAML file is processed. 

```dotenv
url=https://localhost:8000
secret=my_secret_key
```

This approach allows you to reference environment variables directly within your YAML configuration, providing a convenient and secure way to manage sensitive information without exposing it directly in the file


## Development

```sh
make test        # go test ./...
make test-race   # what CI runs
make vet         # go vet ./...
make lint        # golangci-lint, skipped with a note if it is not installed
make cover       # coverage profile plus the total
make build       # the artemis binary
make all         # vet, lint, test, build
```

`make lint` needs golangci-lint, pinned to the version `.golangci.yml` is written for:

```sh
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
```

It is skipped -- with a note, not an error -- when the linter is not on your PATH, so a missing tool never blocks `make build`. Use `make lint-strict` if you want it to fail instead.

### Golden-file tests

`pkg/cli/testdata` holds whole runs: `<case>.yaml` is a scenario a user could have written, `<case>.golden` is every byte artemis printed for it plus the error it exited with. A fixture writes `%SERVER%` where the test's HTTP server goes, and the temp path, the server's port and every duration are normalised before comparison, so the files are stable across machines.

When a change to the report or the runner is deliberate, regenerate them and read the diff:

```sh
make golden      # go test ./pkg/cli -run TestGolden -update
git diff pkg/cli/testdata
```

### CI

`.github/workflows/ci.yml` runs on every push and pull request: `go build ./...`, `go vet ./...`, a gofmt check, `go test -race -coverprofile=coverage.out ./...`, and golangci-lint. The same commands are available as make targets, so a red build is reproducible locally.


## Working:
1. **Sequential and Concurrent Modes**: Introduce support for both sequential and concurrent execution modes. Sequential mode ensures that API requests are executed one after another, while concurrent mode allows for parallel execution of API requests. (Status: In Progress)

2. **Enhanced logging and reporting**: Implement more detailed logging and reporting features to provide deeper insights into test results and execution process. (Status: In Progress)
