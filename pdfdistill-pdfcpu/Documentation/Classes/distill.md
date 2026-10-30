# distill
### Wraps the `pdfdistill-pdfcpu` CLI to shrink PDF files via `4D.SystemWorker`.

> cs.pdfcpu.distill.new ({ class : 4D.Class }) : cs.pdfcpu.distill

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| class | 4D.Class | -> | Optional custom controller class; must extend `cs.pdfcpu._distill_Controller` (default: `cs.pdfcpu._distill_Controller`) |
| Result | cs.pdfcpu.distill | <- | New `distill` instance |

## Description

`cs.pdfcpu.distill` extends [`_CLI`](_CLI.md) and runs the bundled `pdfdistill-pdfcpu` executable (see the [CLI documentation](../../../README.md#command-line-tool) for what it does). It provides `version()` to query the executable and `execute()` to run one or more distill jobs, either synchronously or asynchronously.

The executable is looked up in `/RESOURCES/bin/{platform}/` of the component (`macOS`, `Windows` or `WindowsARM`). If it is not there, the bare name `pdfdistill-pdfcpu` is used and resolved through `$PATH` at runtime.

If `class` does not extend `_distill_Controller` (or is omitted), the constructor silently falls back to `cs.pdfcpu._distill_Controller`. Detection walks the full superclass chain, so indirect subclasses are accepted.

### Properties

| Property | Type | | Description |
| --- | --- | --- | --- |
| onResponse | 4D.Function | | Called when a job completes (set by `execute()` from `events.onResponse`, see [Callbacks](#callbacks)) |
| onError | 4D.Function | | Called on a worker error (set by `execute()` from `events.onError`) |
| onTerminate | 4D.Function | | Called when a worker terminates (set by `execute()` from `events.onTerminate`) |
| onData | 4D.Function | | Set by `execute()` from `events.onData`. Not useful: the CLI emits no progress and partial output is meaningless |
| onDataError | 4D.Function | | Set by `execute()` from `events.onDataError` |
| worker | 4D.SystemWorker | read-only | The currently running worker (`Null` when idle) |
| controller | cs.pdfcpu._distill_Controller | read-only | The attached controller instance |

Properties inherited from [`_CLI`](_CLI.md): `name`, `EOL`, `executableName`, `platform`, `currentDirectory`, `executablePath`, `executableFile`.

## .version()

> .version() : Text

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| Result | Text | <- | Version of the CLI: the short commit hash it was built from, e.g. `"cee530f"` |

Runs `pdfdistill-pdfcpu --version` synchronously and returns the CLI version from the first line of its **stderr** (the CLI writes `--version` and `--help` to stderr, never to stdout). That line reads e.g. `pdfdistill-pdfcpu cee530f (pdfcpu v0.16.4)`; the hexadecimal commit hash is extracted with the regular expression `\s+([a-f0-9]+)\s\(` and the pdfcpu library version is not returned. Returns an empty string if the executable could not be run or the output was not recognised (for example a local build without `-ldflags "-X main.version=<commit>"`, which reports `dev`).

```4d
var $version : Text
$version:=cs.pdfcpu.distill.new().version()  // e.g. "cee530f"
```

## .execute()

> .execute ( tasks : Collection { ; events : Object } ) : Collection

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| tasks | Collection | -> | One task (a collection of arguments), or a collection of tasks |
| events | Object | -> | Callbacks; if `events.onResponse` is a `4D.Function` the call is asynchronous |
| Result | Collection | <- | Synchronous mode: one element per task (see below). Asynchronous mode: `Null` |

Each **task** is a flat collection of arguments that is turned into one `pdfdistill-pdfcpu` command line. Tasks are executed one after another, never in parallel. If `tasks` is a collection whose first element is not itself a collection, it is treated as a single task.

### Task elements

Elements are interpreted by value type:

| Element | Behaviour |
| --- | --- |
| `4D.File` / `4D.Folder` | Its POSIX path is shell-escaped and appended (use for the input file and the `-o` output file) |
| Text `"-o"` / `"--output"` | Appended as-is; the PDF goes to the following path (or to stdout if followed by `"-"`) |
| Text `"-"` | Appended as-is; means "read from stdin" (as INPUT) or "write to stdout" (after `-o`); enables the stdin payload from `.file` |
| Other Text | Shell-escaped and appended (CLI flags such as `"--quality"`, `"--lossless"`, or a path as Text) |
| Number | Converted with `String()` and appended (e.g. the value of `--quality` or `--max-dpi`) |
| Object (not a file/folder) | Context object, see below. Not appended |
| Boolean, Null, Collection | Ignored. Flags must be passed as Text (`"--lossless"`, not `True`) |

The **context object** may have two properties:

| Property | Type | Description |
| --- | --- | --- |
| data | Variant | Any value; passed back to the `onResponse` callback as `$2.context` |
| file | 4D.Blob \| Blob \| Text | Payload written to the CLI's stdin when the task contains `"-"`. Text is sent as UTF-8 bytes (it is **not** a path). To feed a `4D.File` through stdin, pass `$file.getContent()`. If `"-"` is the INPUT but no `file` is given, the CLI waits for stdin indefinitely |

Supported CLI options (see the [CLI documentation](../../../README.md#usage)):

| Option | Value | Default | Description |
| --- | --- | --- | --- |
| `-o` / `--output` | path or `-` | `<input>.distilled.pdf` | Output file |
| `--quality` | 1–100 | 75 | JPEG quality for recompressed images |
| `--max-dpi` | n | 150 | Downsample images above n dpi; `0` = never |
| `--no-images` | | | Skip image recompression |
| `--no-subset` | | | Skip font subsetting |
| `--no-dedup` | | | Skip duplicate object removal |
| `--lossless` | | | Lossless optimizations only |
| `-v` / `--verbose` | | | Per-step size statistics on stderr |
| `-q` / `--quiet` | | | No summary line on stderr |

### Synchronous mode

When `events` is omitted (or has no `onResponse` function), each task runs to completion before the next starts and `execute()` returns a collection with one result object per task:

| Property | Type | Description |
| --- | --- | --- |
| response | 4D.Blob | The CLI's stdout (the distilled PDF when it writes to stdout; empty when writing to a file) |
| responseError | Text | The CLI's stderr (summary line, `-v` statistics or error message) |
| exitCode | Integer | `0` = success, `1` = runtime error, `2` = usage error |

**Blob in / Blob out:** pass `"-"` as INPUT and the PDF as the stdin payload, without `-o` (or with `"-o"; "-"`): `["-"; …options; {file: $blob}]`. The distilled PDF is returned in `response`.

Any callbacks left on the instance by a previous asynchronous call are cleared, so synchronous calls always collect stdout/stderr.

### Asynchronous mode

When `events.onResponse` is a `4D.Function`, the commands are queued and `execute()` returns `Null` immediately. The callbacks are invoked in the calling process, which must stay alive until they fire (e.g. a worker started with `CALL WORKER`, or a process with a form).

| Property | Type | Description |
| --- | --- | --- |
| events.onResponse | 4D.Function | Required. Called once per task when the CLI has finished |
| events.onError | 4D.Function | Optional. Called on a worker error |
| events.onDataError | 4D.Function | Optional. Called when stderr data arrives |
| events.onTerminate | 4D.Function | Optional. Called when a worker terminates |
| events.onData | 4D.Function | Optional. Not useful: the CLI emits no progress and partial stdout is not a usable PDF |

The callback functions are stored on the instance and replaced (or cleared) by the next `execute()` call.

### Callbacks

**onResponse** is called with:

| Parameter | Type | Description |
| --- | --- | --- |
| $1 | 4D.SystemWorker | The finished worker: `.exitCode` (0 = success), `.responseError` (stderr: summary line or error message), `.response` (stdout as `4D.Blob`) |
| $2 | Object | SystemWorker event object (`.type` = `"response"`); `.context` holds the `data` value of the task's context object |

`This` is the controller. The component's own `onResponse` project method shows this signature.

**onError** and **onTerminate** are called with `This` = the `4D.SystemWorker` and `$1` = the SystemWorker event object.

## .terminate()

> .terminate()

Clears the queue of pending tasks and terminates the running worker, if any.

## Examples

### File to file (synchronous)

```4d
var $distill : cs.pdfcpu.distill
$distill:=cs.pdfcpu.distill.new()

var $in; $out : 4D.File
$in:=Folder(fk desktop folder).file("report.pdf")
$out:=Folder(fk desktop folder).file("report.distilled.pdf")

$distill.execute([$in; "-o"; $out; "--quality"; 60; "--max-dpi"; 150])

If ($out.exists)
	ALERT(String($in.size)+" -> "+String($out.size)+" bytes")
End if 
```

### Several files, lossless (synchronous)

```4d
var $tasks : Collection
$tasks:=[]
var $file : 4D.File
For each ($file; Folder(fk desktop folder).folder("PDF").files(fk ignore invisible))
	If ($file.extension=".pdf")
		$tasks.push([$file; "-o"; $file.parent.file($file.name+".min.pdf"); "--lossless"])
	End if 
End for each 

cs.pdfcpu.distill.new().execute($tasks)
```

### Blob to Blob via stdin/stdout (synchronous)

```4d
var $pdf : 4D.Blob
$pdf:=Folder(fk desktop folder).file("report.pdf").getContent()

var $result : Object
$result:=cs.pdfcpu.distill.new().execute(["-"; "--quality"; 60; {file: $pdf}])[0]

If ($result.exitCode=0)
	var $distilled : 4D.Blob
	$distilled:=$result.response
Else 
	ALERT($result.responseError)
End if 
```

### Asynchronous with callback

```4d
#DECLARE($params : Object)

If ($params=Null)
	CALL WORKER(1; Current method name; {})
Else 
	var $in; $out : 4D.File
	$in:=Folder(fk desktop folder).file("report.pdf")
	$out:=Folder(fk desktop folder).file("report.distilled.pdf")
	
	var $events : Object
	$events:={onResponse: Formula(MY_ON_RESPONSE($1; $2))}
	
	cs.pdfcpu.distill.new().execute([[$in; "-o"; $out; "-v"; {data: $out}]]; $events)
End if 
```

```4d
// MY_ON_RESPONSE
#DECLARE($worker : 4D.SystemWorker; $params : Object)

var $out : 4D.File
$out:=$params.context  // the data value of the context object
If ($worker.exitCode=0)
	ALERT($worker.responseError)  // e.g. "/path/report.pdf -> /path/report.distilled.pdf: 720651 -> 116143 bytes (-83.9%)"
Else 
	ALERT("failed: "+$worker.responseError)
End if 
```

## See also

- [`_CLI`](_CLI.md) — parent class providing executable resolution and shell escaping
- [`_distill_Controller`](_distill_Controller.md) — default controller; accumulates output and forwards events
- [`_CLI_Controller`](_CLI_Controller.md) — base controller providing the command queue
