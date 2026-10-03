-- rules/05_rust_binary.lua — build a Rust binary via cargo or rustc.
-- params: name, crate(opt, default "."), features(list,opt),
--         release(bool,opt), manifest(opt, default "Cargo.toml").
local M = {}
M.name = "rust_binary"

local function strlist(v)
  if v == nil then return {} end
  if type(v) == "string" then return { v } end
  if type(v) == "table" then return v end
  error("rust_binary: list param must be string or list")
end

function M.validate(p)
  if p.name == nil then error("rust_binary: missing required param 'name'") end
  return true
end

function M.expand(p)
  M.validate(p)
  local manifest = p.manifest or "Cargo.toml"
  local argv = "cargo build --manifest-path " .. manifest
  if p.release then argv = argv .. " --release" end
  local feats = strlist(p.features)
  if #feats > 0 then argv = argv .. " --features " .. table.concat(feats, ",") end
  local short = p.name:match("([^:]+)$"):gsub("/", "_")
  local out = p.out or ((p.release and "target/release/" or "target/debug/") .. short)
  return { { rule = "rustc", target = out, workdir = ".", argv = argv } }
end

return M
