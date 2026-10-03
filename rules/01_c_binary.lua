-- rules/01_c_binary.lua — compile + link a C executable.
-- params: name, srcs(list), hdrs(list,opt), deps(list,opt), cflags(list,opt),
--         ldflags(list,opt), out(opt), cc(opt, default "cc").
local M = {}
M.name = "cc_binary"

local function req(p, k)
  if p[k] == nil then error("cc_binary: missing required param '" .. k .. "'") end
  return p[k]
end
local function strlist(v, k)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("cc_binary: param '" .. k .. "' must be string or list")
end

function M.validate(p)
  req(p, "name"); req(p, "srcs")
  for _, s in ipairs(strlist(p.srcs, "srcs")) do
    if not s:match("%.c$") then error("cc_binary: src '" .. s .. "' is not a .c file") end
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local cc = p.cc or "cc"
  local cflags = table.concat(strlist(p.cflags, "cflags"), " ")
  local ldflags = table.concat(strlist(p.ldflags, "ldflags"), " ")
  local objs = {}
  for _, s in ipairs(strlist(p.srcs, "srcs")) do
    objs[#objs + 1] = s:gsub("%.c$", ".o")
  end
  local out = p.out or (".ebuild/bin/" .. p.name:match("([^:]+)$"):gsub("/", "_"))
  local argv = cc .. " -o " .. out .. " " .. table.concat(strlist(p.srcs, "srcs"), " ")
  if cflags ~= "" then argv = argv .. " " .. cflags end
  if ldflags ~= "" then argv = argv .. " " .. ldflags end
  return { { rule = "cc", target = out, workdir = ".", argv = argv } }
end

return M
