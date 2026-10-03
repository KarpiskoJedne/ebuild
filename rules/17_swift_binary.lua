-- rules/17_swift_binary.lua — build a Swift executable.
local M = {}
M.name = "swift_binary"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("swift_binary: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("swift_binary: missing required param 'name'") end
  if p.srcs == nil then error("swift_binary: missing required param 'srcs'") end
  for _, s in ipairs(strlist(p.srcs)) do
    if not s:match("%.swift$") then error("swift_binary: src '" .. s .. "' not .swift") end
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/bin/" .. short)
  local argv = "swiftc -o " .. out .. " " .. table.concat(strlist(p.srcs), " ") ..
    " " .. (p.swiftc_args or "")
  return { { rule = "swiftc", target = out, workdir = ".", argv = argv } }
end

return M
