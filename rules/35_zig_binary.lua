-- rules/35_zig_binary.lua — build a Zig binary.
-- params: name, root_src, optimize("Debug"|"ReleaseSafe"|"ReleaseFast"|"ReleaseSmall"),
--         target_triple(opt), out(opt).
local M = {}
M.name = "zig_binary"

function M.validate(p)
  if p.name == nil then error("zig_binary: missing required param 'name'") end
  if p.root_src == nil then error("zig_binary: missing required param 'root_src'") end
  local o = p.optimize or "ReleaseSafe"
  if o ~= "Debug" and o ~= "ReleaseSafe" and o ~= "ReleaseFast" and o ~= "ReleaseSmall" then
    error("zig_binary: bad optimize mode '" .. o .. "'")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("zig-out/bin/" .. short)
  local argv = "zig build-exe " .. p.root_src .. " -O " .. (p.optimize or "ReleaseSafe") ..
    " -femit-bin=" .. out
  if p.target_triple then argv = argv .. " -target " .. p.target_triple end
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
