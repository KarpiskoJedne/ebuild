-- rules/10_python_test.lua — run a Python test suite hermetically.
-- params: name, paths(list,opt, default ["tests"]), runner("pytest"|"unittest").
local M = {}
M.name = "python_test"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("python_test: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("python_test: missing required param 'name'") end
  local r = p.runner or "pytest"
  if r ~= "pytest" and r ~= "unittest" then
    error("python_test: runner must be pytest|unittest")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local paths = strlist(p.paths)
  if #paths == 0 then paths = { "tests" } end
  local argv
  if (p.runner or "pytest") == "unittest" then
    argv = "python -m unittest discover -s " .. table.concat(paths, " ")
  else
    argv = "python -m pytest -q " .. table.concat(paths, " ")
  end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "python", target = ".ebuild/test/" .. short .. ".ok", workdir = ".", argv = argv } }
end

return M
