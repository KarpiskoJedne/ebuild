-- rules/36_nim_binary.lua — compile a Nim binary.
-- params: name, src, out(opt), release(bool,opt, default true).
local M = {}
M.name = "nim_binary"

function M.validate(p)
  if p.name == nil then error("nim_binary: missing required param 'name'") end
  if p.src == nil then error("nim_binary: missing required param 'src'") end
  if not p.src:match("%.nim$") then error("nim_binary: src must be a .nim file") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/bin/" .. short)
  local mode = "-d:release"
  if p.release == false then mode = "-d:debug" end
  local argv = "nim c " .. mode .. " --out:" .. out .. " " .. p.src
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
