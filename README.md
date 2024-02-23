# Artemis

Artemis is a command-line tool for automated testing of REST APIs, built with Go and Cobra. It provides a convenient way to ensure the stability and correctness of your API endpoints through automated testing procedures.

## Features

- Easy-to-use command-line interface (CLI) powered by Cobra
- Supports testing of REST API endpoints
- Customizable testing scenarios and assertions
- Integration with continuous integration (CI) pipelines
- Detailed test reports and logs

## Installation

To install Artemis, make sure you have Go installed and then run:

```bash
source ./install.sh
```

Basic YAML Config
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

Add this when you want to add the variables, so that it'll be saved into the context and can be reused
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

Add assertions
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
## Meta Section

Multiple call of a same api\
type: multiple, single\
max: number (maximum number for times you want to call the api)\
interval: in seconds\
exit: it is a condition for which you want exit polling mode of the api

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