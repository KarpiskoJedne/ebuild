-- rules/43_openapi_codegen.lua — generate clients/servers from OpenAPI specs.
-- params: name, spec, generator (e.g. "go", "python", "typescript-axios"),
--         out_dir(opt).
local M = {}
M.name = "openapi_codegen"

function M.validate(p)
  if p.name == nil then error("openapi_codegen: missing required param 'name'") end
  if p.spec == nil then error("openapi_codegen: missing required param 'spec'") end
  if not (p.spec:match("%.ya?ml$") or p.spec:match("%.json$")) then
    error("openapi_codegen: spec must be .yml/.yaml/.json")
  end
  if p.generator == nil then error("openapi_codegen: missing required param 'generator'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local outdir = p.out_dir or (".ebuild/openapi/" .. short)
  local argv = "openapi-generator-cli generate -i " .. p.spec ..
    " -g " .. p.generator .. " -o " .. outdir
  return { { rule = "gen", target = outdir .. "/.openapi-ok",
             workdir = ".", argv = argv .. " && touch " .. outdir .. "/.openapi-ok" } }
end

return M
