-- rules/12_typescript_library.lua — typecheck + emit declarations for a TS lib.
local M = {}
M.name = "typescript_library"

function M.validate(p)
  if p.name == nil then error("typescript_library: missing required param 'name'") end
  if p.entry == nil then error("typescript_library: missing required param 'entry'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".d.ts")
  local argv = "tsc " .. p.entry .. " --declaration --emitDeclarationOnly --outDir dist " ..
    (p.tsc_args or "")
  return { { rule = "tsc", target = out, workdir = ".", argv = argv } }
end

return M
