-- rules/18_swift_library.lua — build a Swift module (.swiftmodule + .a).
local M = {}
M.name = "swift_library"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("swift_library: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("swift_library: missing required param 'name'") end
  if p.srcs == nil then error("swift_library: missing required param 'srcs'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or (".ebuild/lib/lib" .. short .. ".a")
  local argv = "swiftc -emit-library -emit-module -module-name " .. short ..
    " -o " .. out .. " " .. table.concat(strlist(p.srcs), " ") ..
    " " .. (p.swiftc_args or "")
  return { { rule = "swiftc", target = out, workdir = ".", argv = argv } }
end

return M
