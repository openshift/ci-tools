# Contributing to sentry-go
The following is a set of guidelines for contributing to this package and any
of its plugins. As guidelines rather than rules, feel free to propose changes
to them if you encounter any problems.

#### Table of Contents
[What should I know before I get started?](#what-should-i-know-before-i-get-started)
 - [The Sentry Client SDK](#the-sentry-client-sdk)
 - [The Options Pattern](#the-options-pattern)

[Versioning Policy](#versioning-policy)

[API Style](#api-style)
 - [Provide data through a list of options](#provide-data-through-a-list-of-options)
 - [Use builders for complex configuration options](#use-builders-for-complex-configuration-options)

[Test Coverage](#test-coverage)

[Adding a new Sentry Interface](#adding-a-new-sentry-interface)
 - [Basic Option](#basic-option)
 - [Custom Serialization](#custom-serialization)
 - [Merging Multiple Options](#merging-multiple-options)
 - [Doing last-minute preparation on your option](#doing-last-minute-preparation-on-your-option)
 - [Omitting Options from the Packet](#omitting-options-from-the-packet)

## What should I know before I get started?

### The Sentry Client SDK
Sentry provides [documentation for client developers](https://docs.sentry.io/clientdev/)
on their [docs website](https://docs.sentry.io). If you are looking to add a new feature,
interface or field to this library, you should start by reading the official documentation
to ensure it is compatible with Sentry's API.

### The Options Pattern
There is a great article on [halls-of-valhalla.org](https://halls-of-valhalla.org/beta/articles/functional-options-pattern-in-go,54/)
which explains what the Options Pattern is and how it can be leveraged in Go.

This library makes heavy use of the Options Pattern to enable both the building
of event packets (that are then sent to Sentry) and the configuration of the library's
behaviour.

In addition to configuring the core library, these options can be requested later
from a [`Client`](https://godoc.org/gopkg.in/SierraSoftworks/sentry-go.v0#Client)
using the `GetOption(name)` method, allowing plugins to provide and use their own
option types.

## Versioning Policy
This package uses Semantic Versioning for its public API. We are currently on
version 1 of that public API and will endeavour to avoid the need to bump that
major version at all costs - unless there is absolutely not other cource of action.

Due to the way that this package's API is designed, it should be easily possible
for most implementation details to be changed without affecting the public API and
the behaviour of the various components contained within it.

If we do encounter the need to update the public API in a backwards incompatible
manner, we will leverage [gopkg.in](https://gopkg.in) to provide users of the
old version with consistent access to the version they depend upon as well as
bumping our SemVer major version.


## API Style
This library endeavours to provide a simple API where "doing the right thing" is
easy and obvious. To achieve that, it both limits the number of methods available
on common interfaces and pushes the notion of consistency in all interactions.

Specifically, you will notice two major patterns throughout this package's API:

### Provide data through a list of options
Most of this library's options are configurable or can have sensible default specified
by us. As a result, most user interaction will involve customizing those defaults or
providing optional data. To make this as easy as possible, we use the Options Pattern
when creating clients or capturing events.

##### Example
```go
cl := sentry.NewClient(
    sentry.DSN("..."),
    sentry.Release("v1.0.0"),
    sentry.Logger("root"),
)

cl.Capture(
    sentry.Message("This is an example event"),
    sentry.Level(sentry.Info),
)
```

##### Code
```go
func MyOption() sentry.Option {
    return &myOption{}
}

type myOption struct {

}

func (o *myOption) Class() string {
    return "my.option"
}
```

### Use builders for complex configuration options
As much as possible, we want to avoid forcing users to fill in
complex objects unless absolutely necessary. If sensible defaults
can be selected, or fields are optional, they should be configurable
through a builder interface rather than being a requirement of the
option constructor.

This pattern allows a developer to easily discover fields they may
provide and gain insight into the requirements and options available
to them when using your option.

##### Example
```go
sentry.DefaultBreadcrumbs().
    NewDefault(nil).
    WithMessage("This is an example breadcrumb").
    WithCategory("example").
    WithLevel(sentry.Info)
```

##### Code
When implementing your builder, you should provide a builder interface
whose methods all return the same builder interface. This allows your
builder's methods to be easily chained together.

```go
type MyOptionBuilder interface {
    WithStringProperty(value string) MyOptionBuilder
    WithIntProperty(value int) MyOptionBuilder
}
```

## Test Coverage
The value of test coverage as a metric may be endlessly debated, however
this library places a heavy emphasis on using it as an indicator of poor
test coverage within a module. In addition to high test coverage, we strive
for high assertion coverage (with over 1000 assertions in the current test
suite).

If you are making a pull request on this library, please ensure that you have
implemented a comprehensive set of tests to verify all assumptions about its
behaviour as well as to assert the behaviour of its API. This will ensure that
we more easily catch breaking API changes before they are released into the
wild.

### Running Tests in Development
To make developing high quality tests as easy as possible, we make use of
[GoConvey](http://goconvey.co/). Convey is a test framework and runner which
simplifies writing complex test trees and provides an excellent interface
through which the realtime status of your tests can be viewed.

To use it, just do the following:

```bash
$ go get github.com/smartystreets/goconvey
$ $GOPATH/bin/goconvey --port 8080
```

And then open up your web browser: http://localhost:8080/

## Adding a new Sentry Interface
Sometimes you'll want to take advantage of a Sentry processor which isn't
yet supported by this library. This library makes implementing your own
options trivially easy, not only allowing you to add those new interfaces,
but to replace the default implementations if you don't like the way they
work.

**WARNING** If you're using an option that doesn't implement `Omit()` and
always return `true` then you need to ensure that your `Class()` name matches
one of the valid [Sentry interfaces](https://docs.sentry.io/clientdev/interfaces/).
Failure to do so will result in Sentry responding with an error message.

#### Basic Option
The following is a basic option which can be used in calls to
`sentry.NewClient(...)`, `client.Capture(...)` and `client.With(...)`.
It will be added to the packet under the class name `my_interface` and
will be serialized as a JSON object like `{ "field": "value" }`.

```go
package sentry

// MyOption should create a new instance of your myOption type
// and return it as an Option interface (or derivative thereof).
// You should avoid directly exposing the struct and adopt the
// builder pattern if there is the potential need for additional
// configuration.
func MyOption(field string) Option {
    // If an empty field is invalid, then return a nil option
    // and it will be ignored by the options processor.
    if field == "" {
        return nil
    }

    return &myOption{
        Field: field,
    }
}

type myOption struct {
    Field string `json:"field"`
}

func (i *myOption) Class() string {
    return "my_interface"
}
```

#### Custom Serialization
If you need to serialize your option as something other than a JSON
object, you simply need to implement the `MarshalJSON()` method. This
also applies in situations where your object must be marshalled to
a type other than itself.

```go
import "encoding/json"

func (i *myOption) MarshalJSON() ([]byte, error) {
    return json.Marshal(i.Field)
}
```

#### Merging Multiple Options
Sometimes you won't want to simply replace an option's value if a new
instance of it is provided. In these situations, you'll want to implement
the `Merge()` method which allows you to control how your option behaves
when it encounters another option with the same `Class()`.

```go
import "gopkg.in/SierraSoftworks/sentry-go.v1"

func (i *myOption) Merge(old sentry.Option) sentry.Option {
    if old, ok := old.(*myOption); ok {
        return &myOption{
            Field: fmt.Sprintf("%s,%s", old.Field, i.Field),
        }
    }

    // Replace by default if we don't know how to handle the old type
    return i
}
```

#### Doing last-minute preparation on your option
If your option uses a builder interface to configure its fields before
being sent, then you might want to do some processing just before the
option is embedded in the Packet. This is where the `Finalize()` method
comes in.

`Finalize()` will be called when your option is added to a packet for
transmission, so you can use a chainable builder interface like
`MyOption().WithField("example")`.

```go
import "strings"

func (i *myOption) Finalize() {
    i.Field = strings.TrimSpace(i.Field)
}
```

#### Omitting Options from the Packet
In some situations you might find that you want to not include an
option in the packet after all, perhaps the user hasn't provided all
the required information or you couldn't gather it automatically.

The `Omit()` method allows your option to tell the packet whether or
not to include it. We actually use it internally for things like the DSN
which shouldn't be sent to Sentry in the packet, but which we still want
to read from the options builder.

```go
func (i *myOption) Omit() bool {
    return len(i.Field) == 0
}
```
