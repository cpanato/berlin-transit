# go test -coverprofile format: file:start,end statements count
# Exclude the CLI entrypoint; require >=80% in every executable internal package.
NR == 1 { next }
$1 ~ /\/internal\// {
  package = $1
  sub(/\/[^\/]+:[^:]+$/, "", package)
  statements[package] += $2
  if ($3 > 0) covered[package] += $2
}
END {
  for (package in statements) {
    found = 1
    percent = 100 * covered[package] / statements[package]
    printf "%s: %.1f%%\n", package, percent
    if (percent < 80) failed = 1
  }
  if (!found) { print "No core package coverage found"; exit 1 }
  exit failed
}
