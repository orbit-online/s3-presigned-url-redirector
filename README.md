# s3-presigned-url-redirector

A tiny HTTP server that turns incoming requests into S3 presigned URLs and
redirects the client to them. It lets clients read/write objects in a
private S3 bucket without ever holding AWS credentials themselves.

## Authentication

None. That's up to whichever reverse-proxy you put it behind.

## How it works

The request path is used as the S3 object key, and the HTTP method selects
the S3 operation to presign:

| Method | S3 operation |
| ------ | ------------ |
| GET    | GetObject    |
| HEAD   | HeadObject   |
| PUT    | PutObject    |
| DELETE | DeleteObject |

The server responds with a `307 Temporary Redirect` to the presigned URL.
`POST` and any other method are rejected.

PUT requests without the `Expect: 100-continue` header will result in a
`400 Bad Request` response unless `--proxy-puts` is enabled. In which case
the redirector itself will perform the request to the signed URL, forward the
request body, and respond with the reverse proxied response.  
Conversely if `Expect: 100-continue` is set, it is assumed that the client can
handle an early redirect response and forwarding should work as expected
(more info [here](https://everything.curl.dev/http/post/expect100.html)).

AWS credentials are resolved via the standard AWS SDK credential chain
(environment variables, shared config/profile, EC2/ECS/EKS role, etc.).

## Usage

```
s3-presigned-url-redirector - Redirect S3 requests to presigned URLs
Usage:
  redirector serve BUCKET [--method METHOD...] [options]

Options:
  --ttl SECONDS       Expiry of the signed URLs in seconds [default: 900]
  --addr ADDR         Address to listen on [default: :3000]
  --metrics ADDR      Address to serve metrics on, "" to disable [default: :3001]
  --probes ADDR       Address to serve probes on, "" to disable [default: :3002]
  -m --method METHOD  Methods to whitelist for signing [default: HEAD GET]
  --proxy-puts        Proxy PUT requests without a "Expect: 100-continue" header
```

Example:

```sh
go run . serve my-bucket --addr :8080 --ttl 60 --method HEAD --method GET --method PUT
```

## Metrics

- `s3_presigned_url_redirector_requests_total{method}`
- `s3_presigned_url_redirector_put_request_proxied`
- `s3_presigned_url_redirector_put_bytes_proxied`
- `s3_presigned_url_redirector_requests_denied{method}`
- `s3_presigned_url_redirector_errors_total{errortype}`
