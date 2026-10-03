-- rules/06_rust_library.lua — build a Rust library crate (rlib).
local M = {}
M.name = "rust_library"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("rust_library: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("rust_library: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local manifest = p.manifest or "Cargo.toml"
  local argv = "cargo build --lib --manifest-path " .. manifest
  if p.release then argv = argv .. " --release" end
  local feats = strlist(p.features)
  if #feats > 0 then argv = argv .. " --features " .. table.concat(feats, ",") end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ((p.release and "target/release/" or "target/debug/") ..
    "lib" .. short .. ".rlib")
  return { { rule = "rustc", target = out, workdir = ".", argv = argv } }
end

return M
