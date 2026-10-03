-- rules/04_cpp_library.lua — compile a static C++ library (.a).
local M = {}
M.name = "cxx_library"

local function strlist(v, k)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("cxx_library: param '" .. k .. "' must be string or list")
end

function M.validate(p)
  if p.name == nil then error("cxx_library: missing required param 'name'") end
  if p.srcs == nil then error("cxx_library: missing required param 'srcs'") end
  for _, s in ipairs(strlist(p.srcs, "srcs")) do
    if not (s:match("%.cc$") or s:match("%.cpp$") or s:match("%.cxx$")) then
      error("cxx_library: src '" .. s .. "' is not a C++ file")
    end
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local cxx = p.cxx or "c++"
  local flags = "-std=" .. (p.std or "c++17")
  local extra = table.concat(strlist(p.cflags, "cflags"), " ")
  if extra ~= "" then flags = flags .. " " .. extra end
  local lib = p.out or (".ebuild/lib/lib" .. p.name:match("([^:]+)$"):gsub("/", "_") .. ".a")
  local srcs = strlist(p.srcs, "srcs")
  local objs, steps = {}, {}
  for i, s in ipairs(srcs) do
    local o = ".ebuild/obj/" .. s:gsub("%.%w+$", ".o")
    objs[#objs + 1] = o
    steps[#steps + 1] = {
      rule = "cxx", target = o, workdir = ".",
      argv = cxx .. " " .. flags .. " -c " .. s .. " -o " .. o,
    }
    _ = i
  end
  steps[#steps + 1] = {
    rule = "archive", target = lib, workdir = ".",
    argv = "ar rcs " .. lib .. " " .. table.concat(objs, " "),
  }
  return steps
end

return M
