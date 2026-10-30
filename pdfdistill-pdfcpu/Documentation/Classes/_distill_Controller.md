# _distill_Controller
### Extends `_CLI_Controller` with stdout/stderr accumulation and event forwarding.

> cs.pdfcpu._distill_Controller.new ( CLI : cs.pdfcpu._CLI ) : cs.pdfcpu._distill_Controller

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| CLI | cs.pdfcpu._CLI | -> | The owning [`distill`](distill.md) instance |
| Result | cs.pdfcpu._distill_Controller | <- | New controller |

## Description

Internal class. `_distill_Controller` is the default controller used by [`distill`](distill.md). It inherits the command queue and worker management of [`_CLI_Controller`](_CLI_Controller.md) and overrides the event handlers to:

- accumulate stdout into `stdOut` and stderr into `stdErr`;
- forward `onResponse`, `onError` and `onTerminate` events to the corresponding callbacks of the owning `distill` instance.

To customise event handling, extend this class and pass the subclass to `cs.pdfcpu.distill.new()`.

### Properties

In addition to the properties inherited from `_CLI_Controller`:

| Property | Type | Description |
| --- | --- | --- |
| stdOut | Text | Accumulated stdout of the current command (decoded as UTF-8 text; `dataType` is `"text"`) |
| stdErr | Text | Accumulated stderr of the current command (summary line, verbose statistics, errors) |

## .clear()

> .clear() : cs.pdfcpu._distill_Controller

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| Result | cs.pdfcpu._distill_Controller | <- | `This`, for chaining |

Resets `stdOut` and `stdErr` to empty strings. Called by the constructor and by `distill.execute()` after each synchronous task.

## Event handlers

#### onData ( $worker : 4D.SystemWorker ; $params : Object )

Appends `$params.data` to `stdOut`.

#### onDataError ( $worker : 4D.SystemWorker ; $params : Object )

Appends `$params.data` to `stdErr`.

#### onResponse ( $worker : 4D.SystemWorker ; $params : Object )

Calls `distill.onResponse` (if it is a `4D.Function`) with `This` = `$worker` and `$1` = `$params`.

In asynchronous mode, `distill.execute()` replaces this handler with `events.onResponse`, which is then called with `$1` = `$worker` and `$2` = `$params` (see [`distill`](distill.md#callbacks)).

#### onError ( $worker : 4D.SystemWorker ; $params : Object )

Calls `distill.onError` (if set) with `This` = `$worker` and `$1` = `$params`.

#### onTerminate ( $worker : 4D.SystemWorker ; $params : Object )

Calls `distill.onTerminate` (if set) with `This` = `$worker` and `$1` = `$params`.

## See also

- [`_CLI_Controller`](_CLI_Controller.md) — parent class
- [`distill`](distill.md) — owner whose callbacks this controller forwards to
