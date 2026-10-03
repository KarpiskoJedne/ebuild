-- rules/13_java_binary.lua — compile + jar a Java application.
-- params: name, srcs(list), main_class, out(opt), javac_args(opt).
local M = {}
M.name = "java_binary"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("java_binary: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("java_binary: missing required param 'name'") end
  if p.srcs == nil then error("java_binary: missing required param 'srcs'") end
  if p.main_class == nil then error("java_binary: missing required param 'main_class'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local classes = ".ebuild/classes/" .. short
  local out = p.out or ("dist/" .. short .. ".jar")
  local srcs = table.concat(strlist(p.srcs), " ")
  local argv = "javac -d " .. classes .. " " .. (p.javac_args or "") .. " " .. srcs ..
    " && jar --create --file " .. out .. " --main-class " .. p.main_class ..
    " -C " .. classes .. " ."
  return { { rule = "javac", target = out, workdir = ".", argv = argv } }
end

return M
