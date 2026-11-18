# Memory and skill performance

Measured on Windows/amd64, Intel i7-13700H, Go 1.27.2, using temporary synthetic
workspaces. Figures are medians of three 500 ms benchmark runs with `-cpu=1`.
These are local storage/CPU benchmarks; no providers or running agents are used.
Filesystem caching and concurrent machine activity affect elapsed time.

| Operation | Before | After | Before B/op | After B/op |
| --- | ---: | ---: | ---: | ---: |
| Read 100-file index, warm store | 150.48 us | 96.84 us | 117,786 | 53,482 |
| Read 2,500-file index, warm store | 5.564 ms | 1.079 ms | 3,579,182 | 1,252,684 |
| Read 2,500-file index, cold store | 5.564 ms | 6.618 ms | 3,579,182 | 4,831,371 |
| Construct prompt context, 2,500-file index | 20.545 ms | 18.003 ms | 15,242,592 | 14,044,380 |
| Summarize/hash 16.25 MiB file | 14.314 ms | 11.482 ms | 17,048,475 | 33,589 |
| Match 32 skills with diagnostics | 46.51 us | 24.82 us | 97,760 | 11,296 |
| Match 256 skills with diagnostics | 598.39 us | 193.77 us | 771,552 | 82,976 |

The original index reader is retained as the `decode` benchmark. CPU profiling
of that path showed JSON scanning/decoding among the largest CPU consumers.
The `store` benchmark uses the normal store; `cold` creates a new store for each
read. Cold means an empty application cache, not a cold OS filesystem cache.
The prompt benchmark selects one existing file, refreshes its usage metadata
and writes the index on every iteration. Its before run used the original
index reader in an isolated source copy. Skill matching was also measured with
a pre-change compiled test binary using identical fixtures and flags.

The cache retains one decoded index per store. Eligibility is limited to 4 MiB
of encoded JSON and at most 2,500 files; larger indexes still return in full.
The decoded heap size can exceed the encoded size. Every read still reads the
file and checks its SHA-256, so external edits, same-size/same-timestamp changes,
atomic replacements, missing files, corruption and recovery remain visible.
Returned file and symbol slices are copied. Query results and prompts are not
cached. There is a measured cold-read allocation/time cost; warm reuse offsets
it, including in the prompt benchmark with usage writes.

Large files are hashed to EOF through a bounded streaming buffer. Full SHA-256,
metadata and summary output are preserved. Small files retain their original
summary path. Skill normalization now shares the immutable string replacer;
matching scores, tie breaks, diagnostics and reload behavior are unchanged.

Task retrieval now uses eight readers per invocation when there are more than
32 eligible JSON files and `GOMAXPROCS` is greater than one. Small directories
and single-CPU runtimes retain the serial path. All current task files are
opened/decoded on every call; there is no task cache or durable index. Each
reader writes to its entry's original position. Compaction after joining the
readers preserves the original input to the existing `UpdatedAt` sort, including
timestamp ties and skipped corrupt files. Sorting and limiting still happen
after all reads. Discarded slots are cleared to release partial/duplicate data.

The follow-up task measurements use the same machine and three-run medians.
Warm measurements use `BenchmarkTasksReadPaired` with 500 ms runs, alternating
serial/bounded call order on the exact same files. First-pass measurements use
`BenchmarkTasksReadFirstPass`, `-benchtime=1x`, with newly written fixtures for
each run and mode. Values below are elapsed time for one `Tasks(20)` call.

| Task files / read state | Go CPU budget | Serial baseline | Bounded reader |
| --- | ---: | ---: | ---: |
| 100 / warm | 8 | 7.155 ms | 4.440 ms |
| 1,000 / warm | 8 | 61.237 ms | 23.518 ms |
| 100 / warm | 1 | 3.092 ms | 3.124 ms (serial fallback) |
| 1,000 / warm | 1 | 69.636 ms | 67.958 ms (serial fallback) |
| 1,000 / first pass | 8 | 4,113.526 ms | 342.741 ms |

First-pass times varied widely: serial 2.40–5.04 s, bounded 313–353 ms. The
earlier 1.49–3.91 s observation did not separate first reads from repeat reads;
prewarming reveals the much faster repeat-read baseline. Newly written files
can already be in the OS cache. True cold-disk reads were not measured: no
filesystem cache eviction or host configuration changes were performed.

The default multicore path improves warm 1,000-file reads by about 2.6x and
overlaps first-read waits. It uses up to eight active readers/open files per
call and allows more CPU/thread concurrency. This bounds concurrency per call,
not globally across simultaneous callers. Up to eight decoder buffers can be
live at once. It also adds one boolean per file to track successful decodes.
The initial eight-worker prototype showed no
consistent warm benefit on one CPU (and regressed at 100 files), which is why
that runtime uses the serial fallback. The paired one-CPU figures above are
timing variation between the two serial implementations, not a parallel gain.
No retention rules, context limits, stored data or error policy changed.

Run from the repository root:

```powershell
go test ./internal/memory ./internal/skill -count=1
go vet ./internal/memory ./internal/skill
go test ./internal/memory ./internal/skill -run '^$' -bench 'Benchmark(FileIndexRead|PromptContextFresh|SummarizeLargeFile|MatchWithDiagnostics|NormalizeForMatch)$' -benchmem -benchtime=500ms -count=3 -cpu=1
# Warm reads, including serial baseline and threshold sizes:
go test ./internal/memory -run '^$' -bench '^BenchmarkTasksRead$' -benchmem -benchtime=500ms -count=3 '-cpu=1,8'
# Alternate call order on the same files; use serial-ns/op and bounded-ns/op:
go test ./internal/memory -run '^$' -bench '^BenchmarkTasksReadPaired$' -benchmem -benchtime=500ms -count=3 '-cpu=1,8'
# First read of newly written fixtures; keep benchtime=1x:
go test ./internal/memory -run '^$' -bench '^BenchmarkTasksReadFirstPass$/1000$' -benchmem -benchtime=1x -count=3 -cpu=8
go test ./internal/memory -run '^TestTasks' -count=1 '-cpu=1,8'
```

`tasksSerial` retains the original complete reader as a benchmark/test baseline,
using the same decoder and sorting/limit rules. Warm serial/bounded subbenchmarks
share the same directory; first-pass fixtures have identical contents. The
paired benchmark reports both times separately; its standard `ns/op`, `B/op`
and allocation count describe both calls combined.

`SummarizeLargeFile/read-all` retains the original large-file implementation;
`NormalizeForMatch/fresh-replacer` retains the original normalization. To
reproduce the original full matching and prompt paths without editing the
working tree, copy only the packages needed by these benchmarks to a temporary
module and restore the two original call sites there:

```powershell
$baselineRoot = Join-Path $env:TEMP ('goflow-memory-baseline-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $baselineRoot,(Join-Path $baselineRoot 'internal'),(Join-Path $baselineRoot 'pkg') | Out-Null
Copy-Item -LiteralPath go.mod,go.sum -Destination $baselineRoot
foreach ($package in @('memory','skill','persistence','promptguard')) {
    Copy-Item -LiteralPath (Join-Path 'internal' $package) -Destination (Join-Path $baselineRoot 'internal') -Recurse
}
Copy-Item -LiteralPath 'pkg/schema' -Destination (Join-Path $baselineRoot 'pkg') -Recurse
$sourcePath = Join-Path $baselineRoot 'internal/memory/store.go'
$source = [IO.File]::ReadAllText($sourcePath)
$source = $source.Replace('index, err := s.readFileIndex()', 'var index FileIndex' + "`n" + 'err := readJSONFile(filepath.Join(s.workspaceRoot, fileIndexRelativePath), &index)')
[IO.File]::WriteAllText($sourcePath, $source)
$sourcePath = Join-Path $baselineRoot 'internal/skill/matcher.go'
$source = [IO.File]::ReadAllText($sourcePath)
$source = $source.Replace('return strings.ToLower(matchReplacer.Replace(strings.TrimSpace(input)))', 'replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", "\\", "_")' + "`n" + 'return strings.ToLower(replacer.Replace(strings.TrimSpace(input)))')
[IO.File]::WriteAllText($sourcePath, $source)
Push-Location $baselineRoot
try {
    go test ./internal/memory ./internal/skill -run '^$' -bench 'Benchmark(PromptContextFresh|MatchWithDiagnostics)$' -benchmem -benchtime=500ms -count=3 -cpu=1
} finally {
    Pop-Location
}
```

Focused regression tests cover cache bounds without truncation, independent
returned slices, concurrent readers, workspace/query isolation, edited/corrupt/
restored persisted indexes, full large-file hashes and summaries at the size
boundary, normalization compatibility and skill reload. Existing dirty-marker,
prompt refresh, solution invalidation and lifecycle tests also pass. `go vet`
passes. Race testing could not run here: `CGO_ENABLED=0`, with no C compiler on
PATH (`go test -race` reports that cgo is required).

Task-reader equivalence tests run with CPU budgets of both one and eight.
They cover 0/16/32/33/100 files, timestamp ties, unlimited/limited reads, corrupt
and type-invalid JSON, valid empty/null JSON, ignored extensions/directories,
uppercase extensions, the existing trailing-data behavior, same-metadata edits
outside a prior result limit, repaired files, deletion and directory errors.
Concurrent tests use immutable temporary fixtures, join all callers and mutate
only returned data to check call/workspace isolation.
