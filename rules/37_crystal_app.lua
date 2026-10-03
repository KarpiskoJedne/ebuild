-- rules/37_crystal_app.lua — build a Crystal application.
-- params: name, src, out(opt), release(bool,opt, default true).
local M = {}
M.name = "crystal_app"

function M.validate(p)
  if p.name == nil then error("crystal_app: missing required param 'name'") end
  if p.src == nil then error("crystal_app: missing required param 'src'") end
  if not p.src:match("%.cr$") then error("crystal_app: src must be a .cr file") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/bin/" .. short)
  local argv = "crystal build " .. p.src .. " -o " .. out
  if p.release ~= false then argv = argv .. " --release" end
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
