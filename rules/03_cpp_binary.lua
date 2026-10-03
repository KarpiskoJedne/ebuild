-- rules/03_cpp_binary.lua — compile + link a C++ executable.
-- params: name, srcs(list of .cc/.cpp/.cxx), std(opt, default "c++17"),
--         cflags/ldflags(list,opt), out(opt), cxx(opt, default "c++").
local M = {}
M.name = "cxx_binary"

local function strlist(v, k)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("cxx_binary: param '" .. k .. "' must be string or list")
end

function M.validate(p)
  if p.name == nil then error("cxx_binary: missing required param 'name'") end
  if p.srcs == nil then error("cxx_binary: missing required param 'srcs'") end
  for _, s in ipairs(strlist(p.srcs, "srcs")) do
    if not (s:match("%.cc$") or s:match("%.cpp$") or s:match("%.cxx$")) then
      error("cxx_binary: src '" .. s .. "' is not a C++ file")
    end
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local cxx = p.cxx or "c++"
  local std = "-std=" .. (p.std or "c++17")
  local flags = std
  local extra = table.concat(strlist(p.cflags, "cflags"), " ")
  if extra ~= "" then flags = flags .. " " .. extra end
  local ld = table.concat(strlist(p.ldflags, "ldflags"), " ")
  local out = p.out or (".ebuild/bin/" .. p.name:match("([^:]+)$"):gsub("/", "_"))
  local argv = cxx .. " " .. flags .. " -o " .. out .. " " ..
    table.concat(strlist(p.srcs, "srcs"), " ")
  if ld ~= "" then argv = argv .. " " .. ld end
  return { { rule = "cxx", target = out, workdir = ".", argv = argv } }
end

return M
