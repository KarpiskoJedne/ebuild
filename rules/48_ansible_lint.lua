-- rules/48_ansible_lint.lua — lint Ansible playbooks/roles.
-- params: name, paths(list,opt, default ["."]), profile(opt, default "moderate").
local M = {}
M.name = "ansible_lint"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("ansible_lint: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("ansible_lint: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local paths = strlist(p.paths)
  if #paths == 0 then paths = { "." } end
  local argv = "ansible-lint --profile " .. (p.profile or "moderate") .. " " ..
    table.concat(paths, " ")
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/lint/" .. short .. ".ansible.ok",
             workdir = ".", argv = argv } }
end

return M
