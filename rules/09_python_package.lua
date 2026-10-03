-- rules/09_python_package.lua — hermetic Python wheel build.
-- params: name, srcs(list,opt), backend("uv"|"pip"|"poetry", default "uv"),
--         out(opt).
local M = {}
M.name = "python_package"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("python_package: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("python_package: missing required param 'name'") end
  local b = p.backend or "uv"
  if b ~= "uv" and b ~= "pip" and b ~= "poetry" then
    error("python_package: backend must be uv|pip|poetry, got '" .. b .. "'")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".whl")
  local argv
  if (p.backend or "uv") == "poetry" then
    argv = "poetry build -f wheel -o dist"
  elseif (p.backend or "uv") == "pip" then
    argv = "pip wheel --no-deps --wheel-dir dist ."
  else
    argv = "uv build --wheel --out-dir dist ."
  end
  local srcs = strlist(p.srcs)
  if #srcs > 0 then argv = argv .. " # srcs: " .. table.concat(srcs, ",") end
  return { { rule = "python", target = out, workdir = ".", argv = argv } }
end

return M
