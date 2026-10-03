-- rules/49_security_scan.lua — vulnerability scan (trivy/grype/semgrep).
-- params: name, scanner("trivy"|"grype"|"semgrep"), target_path,
--         severity(opt, default "HIGH,CRITICAL"), fail_on(bool,opt).
local M = {}
M.name = "security_scan"

function M.validate(p)
  if p.name == nil then error("security_scan: missing required param 'name'") end
  local s = p.scanner or "trivy"
  if s ~= "trivy" and s ~= "grype" and s ~= "semgrep" then
    error("security_scan: scanner must be trivy|grype|semgrep")
  end
  if p.target_path == nil then error("security_scan: missing required param 'target_path'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local scanner = p.scanner or "trivy"
  local sev = p.severity or "HIGH,CRITICAL"
  local argv
  if scanner == "grype" then
    argv = "grype " .. p.target_path .. " --fail-on " .. (p.fail_on or "high")
  elseif scanner == "semgrep" then
    argv = "semgrep --config auto --error " .. p.target_path
  else
    argv = "trivy fs --severity " .. sev .. " --exit-code 1 " .. p.target_path
  end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  return { { rule = "gen", target = ".ebuild/security/" .. short .. ".sarif",
             workdir = ".",
             argv = argv .. " --format sarif --output .ebuild/security/" .. short .. ".sarif" } }
end

return M
