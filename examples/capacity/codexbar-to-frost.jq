# Optional producer example. Frost never imports CodexBar payloads.
# Input: one codexbar usage JSON array for explicitly selected accounts.
# Usage: jq --slurpfile bindings examples/capacity/bindings.json -f examples/capacity/codexbar-to-frost.jq
def epoch: try fromdateiso8601 catch null;
def window($usage; $id):
  if $id == "primary" or $id == "secondary" or $id == "tertiary" then $usage[$id]
  else [$usage.extraRateWindows[]? | select(.id == $id) | .window][0] end;
. as $rows |
{
  schema_version: 1,
  generated_at: (now | todateiso8601),
  producer: "codexbar-example",
  pools: [
    $bindings[0][] as $binding |
    [$rows[] | select(.provider == $binding.provider)] as $matches |
    (if ($matches|length) == 1 then $matches[0] else null end) as $row |
    ($row.usage // {}) as $usage |
    {
      id: $binding.pool_id,
      observed_at: ($usage.updatedAt // null),
      valid_until: (if ($usage.updatedAt | epoch) != null then (($usage.updatedAt | epoch) + 900 | todateiso8601) else null end),
      measurement: ($usage.dataConfidence // "unknown"),
      source_status: (if ($matches|length)>1 then "ambiguous_account" elif $row==null then "unavailable" elif $row.error!=null then "error" else "ok" end),
      windows: [$binding.window_ids[] as $id | window($usage; $id) as $w |
        {
          id: $id,
          remaining_percent: (if ($w.usedPercent|type)=="number" and $w.usedPercent>=0 and $w.usedPercent<=100 then (100-$w.usedPercent) else null end),
          resets_at: ($w.resetsAt // null),
          duration_seconds: (if ($w.windowMinutes|type)=="number" then ($w.windowMinutes*60) else null end)
        }
      ]
    }
  ]
}
