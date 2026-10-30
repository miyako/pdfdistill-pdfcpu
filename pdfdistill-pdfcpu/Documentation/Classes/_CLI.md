# _CLI
### Abstract base class for wrapping a platform-native CLI executable.

> cs.lopdf._CLI.new ( executableName : Text ; controller : 4D.Class ) : cs.lopdf._CLI

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| executableName | Text | -> | Base name of the executable (without `.exe` on Windows) |
| controller | 4D.Class | -> | Controller class to attach (default: `cs.lopdf._CLI_Controller`) |
| Result | cs.lopdf._CLI | <- | New instance |

## Description

Internal class. `_CLI` resolves the platform-specific path to a bundled executable and attaches a controller that manages the underlying `4D.SystemWorker`. It is extended by [`distill`](distill.md) and should not be instantiated directly.

### Resolution strategy

The constructor looks for the executable in `/RESOURCES/bin/{platform}/` of the component. If found, its full path is stored (POSIX path on macOS, platform path on Windows) and, on macOS, `chmod +x` is applied. If not found, the bare executable name is stored and resolved through `$PATH` at runtime.

| Platform | `platform` value | Executable |
| --- | --- | --- |
| macOS | `macOS` | `pdfdistill-lopdf` |
| Windows (x64) | `Windows` | `pdfdistill-lopdf.exe` |
| Windows (ARM) | `WindowsARM` | `pdfdistill-lopdf.exe` |

ARM detection on Windows matches `System info.processor` against known ARM processor identifiers: `apple`, `snapdragon`, `qualcomm`, `oryon`, `sq`, `ampere`, `altra`, `neoverse`, `graviton`, `cobalt`, `cortex`, `arm`.

### Properties

All properties are read-only.

| Property | Type | Description |
| --- | --- | --- |
| name | Text | Class name |
| EOL | Text | `"\n"` on macOS, `"\r\n"` on Windows |
| executableName | Text | Executable file name (with `.exe` on Windows) |
| platform | Text | Platform folder name (see table above) |
| currentDirectory | 4D.Folder | `/RESOURCES/bin/{platform}` folder |
| executablePath | Text | Full path of the executable, or its bare name if it is not bundled |
| executableFile | 4D.File | The expected executable file (may not exist) |
| controller | cs.lopdf._CLI_Controller | Attached controller instance |

## .escape()

> .escape ( in : Text ) : Text

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| in | Text | -> | Raw argument |
| Result | Text | <- | Escaped argument |

Escapes a command-line argument for the current platform. On macOS each shell metacharacter (`` \ ! " # $ % & ' ( ) = ~ | < > ? ; * ` [ ] `` and space) is prefixed with `\`. On Windows the argument is wrapped in double quotes when it contains `& | < > ( ) % ^ "` or a space (a trailing `\` is doubled inside the quotes).

## .expand()

> .expand ( in : 4D.File | 4D.Folder ) : 4D.File | 4D.Folder

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| in | 4D.File \| 4D.Folder | -> | File or folder, possibly expressed with a filesystem such as `/RESOURCES` |
| Result | 4D.File \| 4D.Folder | <- | New object of the same class, created from the platform path |

Converts a filesystem-relative file or folder into one with an absolute path.

## .quote()

> .quote ( in : Text ) : Text

| Parameter | Type | | Description |
| --- | --- | --- | --- |
| in | Text | -> | Input text |
| Result | Text | <- | `in` wrapped in double quotes |

## See also

- [`_CLI_Controller`](_CLI_Controller.md) — manages `4D.SystemWorker` execution
- [`distill`](distill.md) — extends `_CLI` to wrap the `pdfdistill-lopdf` executable
