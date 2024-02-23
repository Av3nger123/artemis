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
# API Configuration

## Basic YAML Config

This configuration defines a basic API request to generate a token. It includes the following parameters:

- **name**: Name of the API request.
- **url**: The URL endpoint for the API request, with a placeholder "{{.url}}" that will be replaced with the base URL defined in the configuration section.
- **method**: HTTP method for the request (e.g., POST).
- **headers**: Headers to be included in the request, specifying the Content-Type as "application/json".
- **body**: JSON payload containing the username and password for authentication.

```yaml
configuration:
  url: "https://api.example.com/v2"
apis:
  - name: "Generate Token"
    url: "{{.url}}/token"
    method: "POST"
    headers:
      Content-Type: "application/json"
    body: '{"username":"user_name","password":"password"}'
```

## Adding Variables

This configuration extends the basic API request by adding support for capturing variables from the response. In this case, it captures the access token from the response body and saves it as a variable named "token".

- **variables**: Defines a list of variables to capture from the response.
    - **name**: Name of the variable.
    - **path**: JSON path to locate the variable value in the response.
    - **type**: Data type of the variable (e.g., string).

```yaml
  - name: "Generate Token"
    url: "{{.url}}/token"
    method: "POST"
    headers:
      Content-Type: "application/json"
    body: '{"username":"user_name","password":"password"}'
    variables:
      - name: "token"
        path: "data.token.access_token"
        type: "string"

```

## Adding Assertions

This configuration further enhances the API request by adding assertions to validate the response. It checks if the HTTP status code is 200.

- **assert**: Specifies assertions to be performed on the response.
  - **status**: Expected HTTP status code.

```yaml
  - name: "Generate Token"
    url: "{{.url}}/token"
    method: "POST"
    headers:
      Content-Type: "application/json"
    body: '{"username":"user_name","password":"password"}'
    variables:
      - name: "token"
        path: "data.token.access_token"
        type: "string"
    assert:
        status: 200
```

## User Input

This section allows defining user inputs that can be provided during execution. The input is specified as a list of key-value pairs.

- **key**: Key name for the input field.

```yaml
input:
  - key: username
  - key: password
```

## Meta Section

This section introduces additional metadata for configuring advanced features such as multiple calls of the same API, specifying the maximum number of calls, polling intervals, and exit conditions.

- **type**: Type of API call (e.g., single or multiple).
- **max**: Maximum number of times the API should be called.
- **interval**: Time interval between successive API calls (in seconds).
- **exit**: Condition for exiting the polling mode.
    - **key**: Key to check in the response JSON.
    - **value**: Expected value of the key.
    - *type**: Data type of the value.

```yaml
  - name: "Generate Token"
    url: "{{.url}}/token"
    method: "POST"
    headers:
      Content-Type: "application/json"
    body: '{"username":"user_name","password":"password"}'
    variables:
      - name: "token"
        path: "data.token.access_token"
        type: "string"
    assert:
        status: 200
    meta:
        type: multiple
        max: 10
        interval: 2
        exit: 
            key: "data.isAuthenticated"
            value: "true"
            type: boolean

```

```vbnet
This Markdown file provides a comprehensive explanation of various configurations for API requests, including basic setup, variable capture, assertion checks, and advanced features like polling. Each configuration is explained with its purpose and structure.

```