-- rules/11_typescript_app.lua — typecheck + bundle a TypeScript app.
-- params: name, entry, out(opt), tsc_args(opt).
local M = {}
M.name = "typescript_app"

function M.validate(p)
  if p.name == nil then error("typescript_app: missing required param 'name'") end
  if p.entry == nil then error("typescript_app: missing required param 'entry'") end
  if not p.entry:match("%.tsx?$") then error("typescript_app: entry must be .ts/.tsx") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".js")
  local argv = "tsc --noEmit " .. (p.tsc_args or "") .. " && esbuild " ..
    p.entry .. " --bundle --platform=browser --outfile=" .. out
  return { { rule = "tsc", target = out, workdir = ".", argv = argv } }
end

return M
