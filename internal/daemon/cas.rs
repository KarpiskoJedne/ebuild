//! BLAKE3 content-addressed storage: parallel hashing (rayon) over
//! memory-mapped inputs, Deflate-compressed on-disk shards (`cas/xx/hash`),
//! atomic ingest, and LRU garbage collection.

use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};

pub const HEX_LEN: usize = 64;

pub fn hex(digest: &blake3::Hash) -> String {
    digest.to_hex().to_string()
}

pub fn hash_bytes(data: &[u8]) -> String {
    hex(&blake3::hash(data))
}

/// Stream-hash a file without loading it wholly into RAM.
pub fn hash_file(path: &Path) -> io::Result<(String, u64)> {
    let f = std::fs::File::open(path)?;
    let len = f.metadata().map(|m| m.len()).unwrap_or(0);
    let mut hasher = blake3::Hasher::new();
    let mut f = f;
    hasher.update_reader(&mut f).map_err(io::Error::other)?;
    Ok((hex(&hasher.finalize()), len))
}

/// Hash many files in parallel across the rayon thread pool.
/// Missing/unreadable files yield `Err` in place (order-preserving).
pub fn hash_files_parallel(paths: &[PathBuf]) -> Vec<io::Result<(PathBuf, String, u64)>> {
    use rayon::prelude::*;
    paths
        .par_iter()
        .map(|p| match hash_file(p) {
            Ok((h, n)) => Ok((p.clone(), h, n)),
            Err(e) => Err(e),
        })
        .collect()
}

fn shard(cas: &Path, digest: &str) -> PathBuf {
    cas.join(&digest[..2.min(digest.len())])
}

/// Ingest bytes under their BLAKE3 digest (compressed, atomic, idempotent).
pub fn cas_put(cas: &Path, data: &[u8]) -> io::Result<String> {
    let digest = hash_bytes(data);
    let dir = shard(cas, &digest);
    let dst = dir.join(&digest);
    if dst.exists() {
        return Ok(digest);
    }
    std::fs::create_dir_all(&dir)?;
    let mut enc = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::new(6));
    enc.write_all(data)?;
    let packed = enc.finish()?;
    let tmp = dir.join(format!(".tmp-{}", std::process::id()));
    std::fs::write(&tmp, &packed)?;
    std::fs::rename(&tmp, &dst)?;
    Ok(digest)
}

/// Fetch + decompress a digest. Validates size against `expect_len` when set.
pub fn cas_get(cas: &Path, digest: &str, expect_len: Option<u64>) -> io::Result<Vec<u8>> {
    if digest.len() != HEX_LEN || !digest.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(io::Error::new(io::ErrorKind::InvalidInput, "bad digest"));
    }
    let raw = std::fs::read(shard(cas, digest).join(digest))?;
    let mut dec = flate2::read::GzDecoder::new(&raw[..]);
    let mut out = Vec::new();
    dec.read_to_end(&mut out)?;
    if let Some(n) = expect_len {
        if out.len() as u64 != n {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "CAS length mismatch",
            ));
        }
    }
    // Self-verifying read: digest must reproduce the key.
    if hash_bytes(&out) != digest {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "CAS digest mismatch",
        ));
    }
    Ok(out)
}

pub fn cas_has(cas: &Path, digest: &str) -> bool {
    digest.len() == HEX_LEN && shard(cas, digest).join(digest).exists()
}

/// LRU collection: evict oldest-first by mtime until `usage <= max_bytes`.
/// Returns `(bytes_freed, files_removed)`.
pub fn gc(cas: &Path, max_bytes: u64) -> (u64, usize) {
    let mut files: Vec<(u64, u64, PathBuf)> = Vec::new();
    let mut total = 0u64;
    let Ok(shards) = std::fs::read_dir(cas) else {
        return (0, 0);
    };
    for sh in shards.flatten() {
        let Ok(kids) = std::fs::read_dir(sh.path()) else {
            continue;
        };
        for k in kids.flatten() {
            let Ok(m) = k.metadata() else {
                continue;
            };
            if !m.is_file() {
                continue;
            }
            let mt = m
                .modified()
                .ok()
                .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
                .map(|d| d.as_secs())
                .unwrap_or(0);
            files.push((mt, m.len(), k.path()));
            total += m.len();
        }
    }
    if total <= max_bytes {
        return (0, 0);
    }
    files.sort_by_key(|f| f.0);
    let (mut freed, mut removed) = (0u64, 0usize);
    for (_, size, path) in files {
        if total - freed <= max_bytes {
            break;
        }
        if std::fs::remove_file(&path).is_ok() {
            freed += size;
            removed += 1;
        }
    }
    // Prune newly-emptied shards.
    if let Ok(shards) = std::fs::read_dir(cas) {
        for sh in shards.flatten() {
            let p = sh.path();
            if p.is_dir() {
                let empty = std::fs::read_dir(&p)
                    .map(|mut r| r.next().is_none())
                    .unwrap_or(false);
                if empty {
                    let _ = std::fs::remove_dir(&p);
                }
            }
        }
    }
    (freed, removed)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn put_get_roundtrip() {
        let dir = std::env::temp_dir().join(format!("ebuild-cas-{}", std::process::id()));
        let data = b"hello ebuild cas";
        let d = cas_put(&dir, data).unwrap();
        assert_eq!(d.len(), 64);
        assert!(cas_has(&dir, &d));
        let back = cas_get(&dir, &d, Some(data.len() as u64)).unwrap();
        assert_eq!(back, data);
        // Idempotent re-put.
        assert_eq!(cas_put(&dir, data).unwrap(), d);
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn parallel_hash_order() {
        let dir = std::env::temp_dir().join(format!("ebuild-casp-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let mut paths = Vec::new();
        for i in 0..16 {
            let p = dir.join(format!("f{}.txt", i));
            std::fs::write(&p, format!("content {}", i)).unwrap();
            paths.push(p);
        }
        let out = hash_files_parallel(&paths);
        assert_eq!(out.len(), 16);
        for (i, r) in out.iter().enumerate() {
            let (p, h, n) = r.as_ref().unwrap();
            assert_eq!(p, &paths[i]);
            assert_eq!(*n, format!("content {}", i).len() as u64);
            assert_eq!(h.len(), 64);
        }
        let _ = std::fs::remove_dir_all(&dir);
    }
}
