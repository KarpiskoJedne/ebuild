-- rules/24_helm_chart.lua — lint + package a Helm chart.
-- params: name, chart(opt, default "."), version(opt), out(opt).
local M = {}
M.name = "helm_chart"

function M.validate(p)
  if p.name == nil then error("helm_chart: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local chart = p.chart or "."
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".tgz")
  local argv = "helm lint " .. chart .. " && helm package " .. chart ..
    " --destination dist"
  if p.version then argv = argv .. " --version " .. p.version end
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
