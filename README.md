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

### Command to convert Postman collection to YAML format

An additional feature that i shipped with this is to convert postman collection format to artemis yaml format for faster configuration

```sh
artemis generate -f postman_collection.json
```
**Note**: After generating a YAML file from a Postman collection, manual adjustments might be necessary to tailor the YAML file according to specific requirements. The generated file serves as a starting point and helps in maintaining the structure and format consistent with the original collection.

### Logging

Additionally, a log report will be generated in `app.log` by default, but you can specify a custom log file path using the -l or --log= flag to provide detailed information about the execution process.


For custom log file path:

```sh
artemis test -f sample.yaml -l custom_log_file.log
```

### Environment variables

Artemis supports loading environment variables from a specified `.env` file. This allows you to store sensitive information or configuration-specific details outside of your main configuration files.

For custom env file path:

```sh
artemis test -f sample.yaml -l custom_log_file.log -e dev.env
```
When running Artemis with the -e flag followed by the path to your environment file, Artemis will load the environment variables from that file and make them available during the execution of your tests.

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

## Adding Assertions

This configuration further enhances the API request by adding assertions to validate the response. It checks if the HTTP status code is 200.

- **response**: Specifies assertions to be performed on the response.
  - **status_code**: Expected HTTP status code.
  - **body**: Expected values in response.
    - **path**: The [JSON path](https://support.smartbear.com/alertsite/docs/monitors/api/endpoint/jsonpath.html) in the response body.
    - **value**: Expected value.
    - **type**: Expected value type.

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

- **retry**: Max retry limit when assertions fail.
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
      retry: 5
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


## Working:
1. **Sequential and Concurrent Modes**: Introduce support for both sequential and concurrent execution modes. Sequential mode ensures that API requests are executed one after another, while concurrent mode allows for parallel execution of API requests. (Status: In Progress)

2. **Enhanced logging and reporting**: Implement more detailed logging and reporting features to provide deeper insights into test results and execution process. (Status: In Progress)
