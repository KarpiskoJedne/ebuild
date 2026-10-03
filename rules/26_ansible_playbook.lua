-- rules/26_ansible_playbook.lua — run an Ansible playbook.
-- params: name, playbook, inventory(opt), extra_vars(list,opt), check(bool,opt).
local M = {}
M.name = "ansible_playbook"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("ansible_playbook: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("ansible_playbook: missing required param 'name'") end
  if p.playbook == nil then error("ansible_playbook: missing required param 'playbook'") end
  if not p.playbook:match("%.ya?ml$") then
    error("ansible_playbook: playbook must be a .yml/.yaml file")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local argv = "ansible-playbook " .. p.playbook
  if p.inventory then argv = argv .. " -i " .. p.inventory end
  for _, v in ipairs(strlist(p.extra_vars)) do
    argv = argv .. " -e " .. v
  end
  if p.check then argv = argv .. " --check" end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/ansible/" .. short .. ".ok",
             workdir = ".", argv = argv } }
end

return M
