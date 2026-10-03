-- rules/44_graphql_schema.lua — validate + codegen from a GraphQL schema.
-- params: name, schema, out(opt), tool(opt, default "graphql-codegen").
local M = {}
M.name = "graphql_schema"

function M.validate(p)
  if p.name == nil then error("graphql_schema: missing required param 'name'") end
  if p.schema == nil then error("graphql_schema: missing required param 'schema'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/graphql/" .. short .. ".gen.ts")
  local tool = p.tool or "graphql-codegen"
  local argv = tool .. " --schema " .. p.schema .. " --out " .. out
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
