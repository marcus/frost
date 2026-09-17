# External producer example. Frost never imports CodexBar payloads.
# Input: a JSON array of CodexBar `usage --json` rows (one per provider run),
# concatenated by refresh.sh. Bindings tell the converter which provider
# feeds which Frost pool and which CodexBar window IDs to read.
# Usage: jq --slurpfile bindings capacity-bindings.json -f codexbar-to-frost.jq
def epoch: try fromdateiso8601 catch null;
def window($usage; $id):
  if $id == "primary" or $id == "secondary" or $id == "tertiary" then $usage[$id]
  else [$usage.extraRateWindows[]? | select(.id == $id) | .window][0] end;
# CodexBar confidences: exact and percentOnly (percentages without token
# counts) are exact percentages; estimated stays estimated. Anything else
# falls back to the binding's declared measurement, then unknown.
def measurement($binding; $row; $usage):
  ($usage.dataConfidence // "") as $dc |
  if $dc == "exact" or $dc == "percentOnly" then "exact"
  elif $dc == "estimated" then "estimated"
  elif $binding.measurement != null then $binding.measurement
  else "unknown" end;
. as $rows |
{
  schema_version: 1,
  generated_at: (now | todateiso8601),
  producer: "codexbar-example",
  pools: [
    $bindings[0][] as $binding |
    [$rows[] | select(.provider == $binding.provider and ($binding.source == null or .source == $binding.source))] as $matches |
    (if ($matches|length) == 1 then $matches[0] else null end) as $row |
    ($row.usage // {}) as $usage |
    {
      id: $binding.pool_id,
      source_status: (if ($matches|length) > 1 then "ambiguous_account"
                      elif $row == null then "unavailable"
                      elif ($row.error != null) or ($row.usage == null) then "error"
                      else "ok" end),
      measurement: measurement($binding; $row; $usage),
      observed_at: ($usage.updatedAt // null),
      valid_until: (if ($usage.updatedAt | epoch) != null then (($usage.updatedAt | epoch) + 900 | todateiso8601) else null end),
      windows: [$binding.window_ids[] as $id | window($usage; $id) as $w |
        {
          id: $id,
          remaining_percent: (if ($w.usedPercent|type) == "number" and $w.usedPercent >= 0 and $w.usedPercent <= 100 then (100 - $w.usedPercent) else null end),
          resets_at: ($w.resetsAt // null),
          duration_seconds: (if ($w.windowMinutes|type) == "number" then ($w.windowMinutes * 60) else null end)
        }
      ]
    }
  ]
}
