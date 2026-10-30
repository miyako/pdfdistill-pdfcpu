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
| onData | 4D.Function | | Stored by `execute()` from `events.onData` but **currently never called** (the CLI does not report progress) |
| worker | 4D.SystemWorker | read-only | The currently running worker (`Null` when idle) |
| controller | cs.pdfcpu._distill_Controller | read-only | The attached controller instance |

Properties inherited from [`_CLI`](_CLI.md): `name`, `EOL`, `executableName`, `platform`, `currentDirectory`, `executablePath`, `executableFile`.

## .version()

> .version() : Text

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| Result | Text | <- | Version number of the CLI, e.g. `"0.1.0"` |

Runs `pdfdistill-pdfcpu --version` synchronously and returns the trailing version number of the first output line. Returns an empty string if the executable could not be run or the output was not recognised.

```4d
var $version : Text
$version:=cs.pdfcpu.distill.new().version()
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
| Text `"-o"` / `"--output"` | Appended as-is; marks the task as writing to a file, so its result is `Null` |
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

When `events` is omitted (or has no `onResponse` function), each task runs to completion before the next starts and `execute()` returns a collection with one element per task:

* `Null` if the task contains `-o` / `--output`;
* otherwise the CLI's stdout decoded as UTF-8 **Text** (an empty string when the CLI writes to the default `<input>.distilled.pdf`).

Because stdout is captured as text, **PDF output to stdout is not usable from 4D** (binary data would be corrupted). Always pass `"-o"` followed by a `4D.File` and read the file afterwards.

The CLI's diagnostics (summary line, errors) go to stderr and are not returned in synchronous mode. Check that the output file exists, or use asynchronous mode to inspect `$worker.exitCode` and `$worker.responseError`.

### Asynchronous mode

When `events.onResponse` is a `4D.Function`, the commands are queued and `execute()` returns `Null` immediately. The callbacks are invoked in the calling process, which must stay alive until they fire (e.g. a worker started with `CALL WORKER`, or a process with a form).

| Property | Type | Description |
| --- | --- | --- |
| events.onResponse | 4D.Function | Required. Called once per task when the CLI has finished |
| events.onError | 4D.Function | Optional. Called on a worker error |
| events.onTerminate | 4D.Function | Optional. Called when a worker terminates |
| events.onData | 4D.Function | Optional. Stored but currently never called |

The callback functions are stored on the instance (and the controller) and remain set for later calls; use a new `distill` instance for each asynchronous job.

### Callbacks

**onResponse** is called with:

| Parameter | Type | Description |
| --- | --- | --- |
| $1 | 4D.SystemWorker | The finished worker: `.exitCode` (0 = success), `.responseError` (stderr: summary line or error message), `.response` (stdout as Text) |
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

### Blob to file via stdin (synchronous)

```4d
var $pdf : 4D.Blob
$pdf:=Folder(fk desktop folder).file("report.pdf").getContent()

var $out : 4D.File
$out:=Folder(fk desktop folder).file("report.distilled.pdf")

cs.pdfcpu.distill.new().execute(["-"; "-o"; $out; {file: $pdf}])

var $distilled : 4D.Blob
$distilled:=$out.getContent()
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
	ALERT($worker.responseError)  // e.g. "pdfdistill-pdfcpu: 720651 -> 114690 bytes (84.1% smaller) -> …"
Else 
	ALERT("failed: "+$worker.responseError)
End if 
```

## See also

- [`_CLI`](_CLI.md) — parent class providing executable resolution and shell escaping
- [`_distill_Controller`](_distill_Controller.md) — default controller; accumulates output and forwards events
- [`_CLI_Controller`](_CLI_Controller.md) — base controller providing the command queue
