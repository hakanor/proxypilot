#!/bin/bash
# Regenerates docs/cli.md from the binary's --help output.
B=./bin/proxypilot
list() { "$@" --help 2>&1 | sed -n '/Available Commands/,/^$/p' | awk 'NR>1 && $1!="" {print $1}' | grep -v -E '^(help|completion)$'; }
help() { "$@" --help 2>&1 | sed '/^Global Flags:/,$d' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}'; }
echo "# ProxyPilot CLI reference"
echo
echo "Generated from \`proxypilot <command> --help\` (regenerate: \`make docs\`). Every command takes the global flags at the end; \`--json\` gives machine-readable output (schemas in [cli-json.md](cli-json.md))."
echo
for c in $(list $B); do
  echo "## \`proxypilot $c\`"; echo; echo '```text'; help $B $c; echo '```'; echo
  for s in $(list $B $c); do echo "### \`proxypilot $c $s\`"; echo; echo '```text'; help $B $c $s; echo '```'; echo; done
done
echo "## Global flags"; echo; echo '```text'; $B --help | sed -n '/^Flags:/,$p' | sed '/^Use "/d'; echo '```'; echo
echo "## Exit codes"; echo
echo "| Code | Name | Meaning |"; echo "|---|---|---|"
echo "| 0 | ExitOK | Success |"
echo "| 1 | ExitError | Something failed (message on stderr) |"
echo "| 2 | ExitUsage | Wrong flags or arguments |"
echo "| 3 | ExitNoDaemon | The daemon is not running (\`proxypilot start --detach\`) |"
echo "| 4 | ExitNotFound | The flow, rule or breakpoint does not exist |"
echo "| 5 | ExitTimeout | \`wait\` / breakpoint wait timed out |"
echo "| 6 | ExitUnauthorized | The control API rejected the token |"
