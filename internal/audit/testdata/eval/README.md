# KodeKavach eval corpus

Each `.txt` file is one case: a small repo and the rules KodeKavach must report for it.

```
# what this case shows
expect: rule-id another-rule-id
-- path/in/repo --
file contents
-- another/path --
...
```

`expect:` lists every rule the case must report. A rule that is reported but not listed is a false positive and fails
the test, so a case with an empty `expect:` is a clean file that must stay silent. `TestEvalCorpusCoversEveryRule`
fails when a rule has no case that reports it, so a new rule needs a case here.

Cases are plain text on purpose: the repo's own CI scanners do not treat the workflow and agent files inside them as
real ones.
