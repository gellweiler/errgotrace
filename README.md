## errgotrace

### Error tracing for go programs

Finding the origin of errors in go programs can be really difficult, compared to other programming languages.
There are projects like [jujo/errgo](https://github.com/juju/errgo) that allow you to wrap errors before returning
them and then get a stack trace for these. For that to work tough, you must use them consistently throughout your project
and that won't help you with errors in 3rd party libraries or with errors that get handled and never get returned.

Errgotrace will add debug code to a go program that logs every returned error value (value that implements the error interface)
and the function in which the error was returned. With that information it is usually easy to find the origin of errors.

### Sample Output

This is what the output of a program compiled with errgotrace looks like.

    [...]
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectKey: EOF token found
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectItem: EOF token found
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectKey: EOF token found
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectItem: EOF token found
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectKey: At 3:4: nested object expected: LBRACE got: ASSIGN
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectItem: At 3:4: nested object expected: LBRACE got: ASSIGN
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.objectList: At 3:4: nested object expected: LBRACE got: ASSIGN
    2017/12/13 00:54:39 [ERRGOTRACE] parser.*Parser.Parse: At 2:31: literal not terminated
    2017/12/13 00:54:39 [ERRGOTRACE] parser.Parse: At 2:31: literal not terminated
    2017/12/13 00:54:39 [ERRGOTRACE] hcl.parse: At 2:31: literal not terminated
    2017/12/13 00:54:39 [ERRGOTRACE] hcl.ParseBytes: At 2:31: literal not terminated
    2017/12/13 00:54:39 [ERRGOTRACE] formula.parse: parsing failed
    [...]

### Usage

**Please make a backup of your project before using errgotrace**

To debug all go files in the current working directory:

    $ find . -name '*.go' -print0 | xargs -0 errgotrace -w

To reverse the changes made to the go files:

    $ find . -name '*.go' -print0 | xargs -0 errgotrace -w -r

CMD-usage:

    Errgotrace modifies go files to include code for tracing go errors.

    usage: errgotrace [flags] [path ...]
      -exclude string
            exclude any matching functions, takes precedence over filter
      -exported
            only annotate exported functions
      -filter string
            only annotate functions matching the regular expression (default ".")
      -r	reverse the process, remove tracing code
      -w	re-write files in place

    Examples:
      Add tracing code to all go files in the current directory.
      $ find . -name '*.go' -print0 | xargs -0 errgotrace -w

      Add tracing code to all go files in the current directory.
      Exclude vendor dir.
      $ find . -path ./vendor -prune -o -name '*.go' -print0 | xargs -0 errgotrace -w

      Remove all tracing code from all go files in the current directory.
      $ find . -path ./vendor -prune -o -name '*.go' -print0 | xargs -0 errgotrace -w -r

### Advanced Logging

If you need better/advanced logging just alter the code in `log/log.go` to your needs.

### Credits

This project is inspired by and uses code from [gotrace](https://github.com/jbardin/gotrace) by James Bardin.
