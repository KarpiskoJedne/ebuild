-- rules/42_latex_document.lua — compile a LaTeX document (latexmk).
-- params: name, main, out(opt), engine("pdflatex"|"xelatex"|"lualatex").
local M = {}
M.name = "latex_document"

function M.validate(p)
  if p.name == nil then error("latex_document: missing required param 'name'") end
  if p.main == nil then error("latex_document: missing required param 'main'") end
  if not p.main:match("%.tex$") then error("latex_document: main must be a .tex file") end
  local e = p.engine or "pdflatex"
  if e ~= "pdflatex" and e ~= "xelatex" and e ~= "lualatex" then
    error("latex_document: bad engine '" .. e .. "'")
  end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("dist/" .. short .. ".pdf")
  local argv = "latexmk -" .. (p.engine or "pdflatex") .. " -interaction=nonstopmode " ..
    "-output-directory=dist " .. p.main .. " && mv dist/" ..
    p.main:gsub("%.tex$", ".pdf") .. " " .. out
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
