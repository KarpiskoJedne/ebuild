-- rules/02_c_library.lua — compile a static C library (.a).
-- params: name, srcs(list), hdrs(list,opt), cflags(list,opt), out(opt).
local M = {}
M.name = "cc_library"

local function strlist(v, k)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("cc_library: param '" .. k .. "' must be string or list")
end

function M.validate(p)
  if p.name == nil then error("cc_library: missing required param 'name'") end
  if p.srcs == nil then error("cc_library: missing required param 'srcs'") end
  for _, s in ipairs(strlist(p.srcs, "srcs")) do
    if not s:match("%.c$") then error("cc_library: src '" .. s .. "' is not a .c file") end
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local cc = p.cc or "cc"
  local cflags = table.concat(strlist(p.cflags, "cflags"), " ")
  local lib = p.out or (".ebuild/lib/lib" .. p.name:match("([^:]+)$"):gsub("/", "_") .. ".a")
  local objs = {}
  for _, s in ipairs(strlist(p.srcs, "srcs")) do
    local o = ".ebuild/obj/" .. s:gsub("%.c$", ".o")
    objs[#objs + 1] = o
    local cmd = cc .. " -c " .. s .. " -o " .. o
    if cflags ~= "" then cmd = cmd .. " " .. cflags end
  end
  local steps = {}
  for i, s in ipairs(strlist(p.srcs, "srcs")) do
    local cmd = cc .. " -c " .. s .. " -o " .. objs[i]
    if cflags ~= "" then cmd = cmd .. " " .. cflags end
    steps[#steps + 1] = { rule = "cc", target = objs[i], workdir = ".", argv = cmd }
  end
  steps[#steps + 1] = {
    rule = "archive", target = lib, workdir = ".",
    argv = "ar rcs " .. lib .. " " .. table.concat(objs, " "),
  }
  return steps
end

return M
