# sentry-go [![Build Status](https://travis-ci.org/SierraSoftworks/sentry-go.svg?branch=master)](https://travis-ci.org/SierraSoftworks/sentry-go) [![](https://godoc.org/gopkg.in/SierraSoftworks/sentry-go.v1?status.svg)](http://godoc.org/gopkg.in/SierraSoftworks/sentry-go.v1) [![codecov](https://codecov.io/gh/SierraSoftworks/sentry-go/branch/master/graph/badge.svg)](https://codecov.io/gh/SierraSoftworks/sentry-go)
**A robust Sentry client for Go applications**

This library is a re-imagining of how Go applications should interact
with a Sentry server. It aims to offer a concise, easy to understand and
easy to extend toolkit for sending events to Sentry, with a strong emphasis
on being easy to use.

## Features
 - **A beautiful API** which makes it obvious exactly what the best way to
   solve a problem is.
 - **Comprehensive** coverage of the various objects that can be sent to Sentry
   so you won't be left wondering why everyone else gets to play with Breadcrumbs
   but you still can't...
 - **StackTrace Support** using the official `pkg/errors` stacktrace provider,
   for maximum compatibility and easy integration with other libraries.
 - **HTTP Context Helpers** to let you quickly expose HTTP request context as
   part of your errors - with optional support for sending cookies, headers and
   payload data.
 - **Extensive documentation** which makes figuring out the right way to use
   something as easy as possible without the need to go diving into the code.

In addition to the features listed above, the library offers support for a number
of more advanced use cases, including sending events to multiple different Sentry
DSNs, derived client contexts, custom interface types and custom transports.

## Versions
This package follows SemVer and uses [gopkg.in](https://gopkg.in) to provide access
to those versions.

 - [sentry-go.v0](https://gopkg.in/SierraSoftworks/sentry-go.v0) - `import ("gopkg.in/SierraSoftworks/sentry-go.v0")`

   This version is the latest `master` branch. You should avoid depending on this version unless you
   are performing active development against `sentry-go`.
 - [**sentry-go.v1**](https://gopkg.in/SierraSoftworks/sentry-go.v1) - `import ("gopkg.in/SierraSoftworks/sentry-go.v1")`

   This version is the most recent release of `sentry-go` and will maintain API compatibility. If you
   are creating a project that relies on `sentry-go` then this is the version you should use.

## Examples

### Breadcrumbs and Exceptions
```go
package main

import (
    "fmt"

    "gopkg.in/SierraSoftworks/sentry-go.v1"
    "github.com/pkg/errors"
)

func main() {
    sentry.AddDefaultOptions(
        sentry.DSN("..."), // If you don't override this, it'll be fetched from $SENTRY_DSN
        sentry.Release("v1.0.0"),
    )

    cl := sentry.NewClient()

    sentry.DefaultBreadcrumbs().NewDefault(nil).WithMessage("Application started").WithCategory("log")

    err := errors.New("error with a stacktrace")

    id := cl.Capture(
        sentry.Message("Example exception submission to Sentry"),
        sentry.ExceptionForError(err),
    ).Wait().EventID()
    fmt.Println("Sent event to Sentry: ", id)
}
```

### HTTP Request Context
```go
package main

import (
    "net/http"
    "os"
    
    "gopkg.in/SierraSoftworks/sentry-go.v1"
)

func main() {
    cl := sentry.NewClient(
        sentry.Release("v1.0.0"),
    )

    http.HandleFunc("/", func(res http.ResponseWriter, req *http.Request) {
        cl := cl.With(
            sentry.HTTPRequest(req).WithHeaders(),
        )

        res.Header().Set("Content-Type", "application/json")
        res.WriteHeader(404)
        res.Write([]byte(`{"error":"Not Found","message":"We could not find the route you requested, please check your URL and try again."}`))

        cl.Capture(
            sentry.Message("Route Not Found: [%s] %s", req.Method, req.URL.Path),
            sentry.Level(sentry.Warning),
        )
    })

    if err := http.ListenAndServe(":8080", nil); err != nil {
        cl.Capture(
            sentry.ExceptionForError(err),
            sentry.Level(sentry.Fatal),
            sentry.Extra(map[string]interface{}{
                "port": 8080,
            }),
        )

        os.Exit(1)
    }
}
```

## Advanced Use Cases

### Custom SendQueues
The default send queue provided by this library is a serial, buffered, queue
which waits for a request to complete before sending the next. This works well
to limit the potential for clients DoSing your Sentry server, but might not
be what you want.

For situations where you'd prefer to use a different type of queue algorithm,
this library allows you to change the queue implementation both globally and
on a per-client basis. You may also opt to use multiple send queues spread
between different clients to impose custom behaviour for different portions
of your application.

```go
import "gopkg.in/SierraSoftworks/sentry-go.v1"

func main() {
    // Configure a new global send queue
    sentry.AddDefaultOptions(
        sentry.UseSendQueue(sentry.NewSequentialSendQueue(10)),
    )

    cl := sentry.NewClient()
    cl.Capture(sentry.Message("Sent over the global queue"))

    // Create a client with its own send queue
    cl2 := sentry.NewClient(
        UseSendQueue(sentry.NewSequentialSendQueue(100)),
    )
    cl2.Capture(sentry.Message("Sent over the client's queue"))
}
```

SendQueue implementations must implement the `SendQueue` interface, which
requires it to provide both the `Enqueue` and `Shutdown` methods.
