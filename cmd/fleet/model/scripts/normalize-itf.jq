# ITF -> a stable, readable trace: module prefixes stripped, sums rendered as
# Tag or Tag(Inner), sets as sorted lists, bigints as numbers.
def render:
  if type == "object" and has("#bigint") then (.["#bigint"] | tonumber)
  elif type == "object" and has("#set") then (.["#set"] | map(render) | sort)
  elif type == "object" and has("tag") then
    (if (.value | type) == "object" and (.value | has("tag")) and .value.tag != "UNIT"
     then "\(.tag)(\(.value | render))" else .tag end)
  else . end;
[.states[]
  | with_entries(select(.key != "#meta"))
  | with_entries(.key |= (split("::") | last))
  | with_entries(.value |= render)]
