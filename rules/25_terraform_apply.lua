-- rules/25_terraform_apply.lua — init + validate + apply a Terraform stack.
-- params: name, dir(opt, default "."), vars(list of KEY=VAL,opt), auto_approve.
local M = {}
M.name = "terraform_apply"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("terraform_apply: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("terraform_apply: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local dir = p.dir or "."
  local varflags = {}
  for _, v in ipairs(strlist(p.vars)) do varflags[#varflags + 1] = "-var " .. v end
  local argv = "terraform -chdir=" .. dir .. " init -input=false && " ..
    "terraform -chdir=" .. dir .. " validate && " ..
    "terraform -chdir=" .. dir .. " apply -input=false " ..
    table.concat(varflags, " ")
  if p.auto_approve ~= false then argv = argv .. " -auto-approve" end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/terraform/" .. short .. ".ok",
             workdir = ".", argv = argv } }
end

return M
