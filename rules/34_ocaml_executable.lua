-- rules/34_ocaml_executable.lua — build an OCaml executable via dune.
-- params: name, exe, out(opt).
local M = {}
M.name = "ocaml_executable"

function M.validate(p)
  if p.name == nil then error("ocaml_executable: missing required param 'name'") end
  if p.exe == nil then error("ocaml_executable: missing required param 'exe'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ("_build/default/" .. p.exe)
  local argv = "dune build " .. p.exe .. " && dune install --prefix " ..
    ".ebuild/ocaml/" .. short .. " " .. p.exe
  return { { rule = "gen", target = out, workdir = ".", argv = argv } }
end

return M
