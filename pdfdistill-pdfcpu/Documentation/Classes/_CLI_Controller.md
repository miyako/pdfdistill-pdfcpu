# _CLI_Controller
### Manages a queue of `4D.SystemWorker` commands for a `_CLI` instance.

> cs.lopdf._CLI_Controller.new ( CLI : cs.lopdf._CLI ) : cs.lopdf._CLI_Controller

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| CLI | cs.lopdf._CLI | -> | The owning `_CLI` instance |
| Result | cs.lopdf._CLI_Controller | <- | New controller |

## Description

Internal class. `_CLI_Controller` runs an ordered queue of command lines, each in its own `4D.SystemWorker`, starting the next one only after the previous worker has responded. The controller object itself is passed as the SystemWorker options object, so its properties (`dataType`, `encoding`, `variables`, `timeout`, `hideWindow`, `currentDirectory`) and event handlers configure each worker. It is extended by [`_distill_Controller`](_distill_Controller.md) and not used directly by application code.

### Properties

| Property | Type | | Description |
| --- | --- | --- | --- |
| dataType | Text | | Worker data type (default `"text"`) |
| encoding | Text | | Text encoding (default `"UTF-8"`) |
| variables | Object | | Environment variables for each worker (default `{}`) |
| timeout | Variant | | Worker timeout in seconds (default `Null` = no timeout) |
| hideWindow | Boolean | | Hide the console window on Windows (default `True`) |
| currentDirectory | 4D.Folder | | Working directory of each worker (default: the owning `_CLI`'s `currentDirectory`) |
| SYSTEM_WORKER_CONTEXT | Object | | Per-command context values, keyed by worker PID |
| complete | Boolean | read-only | `True` once the queue has been drained |
| worker | 4D.SystemWorker | read-only | The running worker (`Null` when idle) |
| commands | Collection | read-only | Pending command lines |
| instance | cs.lopdf._CLI | read-only | The owning `_CLI` instance |

### Event handlers

The properties `onData`, `onDataError`, `onError`, `onResponse` and `onTerminate` are the SystemWorker callbacks. Any that is not defined as a function in a subclass defaults to a no-op handler. While a queue is running, `onResponse` and `onTerminate` are temporarily wrapped so that the next command starts after each response; the original handlers are still called with `This` = the controller, `$1` = the worker and `$2` = the event object (for `onResponse`, `$2.context` is the context value of that command). They are restored when the queue is drained or terminated.

## .execute()

> .execute ( command : Text | Collection { ; message : Variant { ; context : Variant } } ) : cs.lopdf._CLI_Controller

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| command | Text \| Collection | -> | Command line, or a collection of command lines |
| message | Variant | -> | stdin payload, or a collection of payloads (one per command) |
| context | Variant | -> | Context value, or a collection of values (one per command), returned as `$2.context` in `onResponse` |
| Result | cs.lopdf._CLI_Controller | <- | `This`, for chaining |

Appends the command(s) to the queue and starts the first one if no worker is running.

The stdin payload is written and the input closed right after the worker starts: Objects and Collections are sent as JSON, `4D.Blob`/Blob and Text as-is, numbers, Booleans, dates and times through `String()`. `Null` sends nothing and leaves stdin open.

## .terminate()

> .terminate()

Clears the queue, terminates the running worker and restores the original event handlers.

## See also

- [`_CLI`](_CLI.md) — owns and instantiates the controller
- [`_distill_Controller`](_distill_Controller.md) — subclass used by `distill`
