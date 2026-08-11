//! Windows Job Object：确保 core 子进程随壳退出而被杀，杜绝孤儿进程/端口残留。
//!
//! 原理：把 core 管理器进程挂到一个 Job 上，并设置 `KILL_ON_JOB_CLOSE`。
//! 当壳进程退出（含强杀/Ctrl+C/崩溃）时，系统自动关闭 Job 句柄 → Job 内所有
//! 进程（core 及其派生的 sing-box/实例）一并终止，端口全部释放。
//!
//! 非 Windows 平台为空实现（原样返回子进程句柄）。

use std::process::Child;

/// 进程句柄包装：持有 Job 句柄直到壳退出（Drop 时关闭 → 触发 KILL_ON_JOB_CLOSE）。
pub struct JobObject {
    #[cfg(windows)]
    handle: windows_sys_job::HANDLE,
}

impl JobObject {
    /// 创建 Job 并尝试把子进程加入。失败时返回 None（不阻断启动，仅失去防孤儿保护）。
    #[allow(clippy::new_without_default)]
    pub fn new() -> Option<Self> {
        #[cfg(windows)]
        {
            unsafe {
                let handle = windows_sys_job::CreateJobObjectW(std::ptr::null(), std::ptr::null());
                if handle == 0 {
                    return None;
                }
                let mut info: windows_sys_job::JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
                info.BasicLimitInformation.LimitFlags = windows_sys_job::JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
                let ok = windows_sys_job::SetInformationJobObject(
                    handle,
                    windows_sys_job::JOB_OBJECT_EXTENDED_LIMIT_INFORMATION,
                    &info as *const _ as *const core::ffi::c_void,
                    std::mem::size_of::<windows_sys_job::JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
                );
                if ok == 0 {
                    windows_sys_job::CloseHandle(handle);
                    return None;
                }
                Some(JobObject { handle })
            }
        }
        #[cfg(not(windows))]
        {
            None
        }
    }

    /// 把子进程加入 Job。失败不影响主流程（记录日志由调用方决定）。
    pub fn assign(&self, child: &Child) {
        #[cfg(windows)]
        {
            use std::os::windows::io::AsRawHandle;
            unsafe {
                let h = child.as_raw_handle() as windows_sys_job::HANDLE;
                windows_sys_job::AssignProcessToJobObject(self.handle, h);
            }
        }
        #[cfg(not(windows))]
        {
            let _ = child;
        }
    }
}

impl Drop for JobObject {
    fn drop(&mut self) {
        #[cfg(windows)]
        unsafe {
            windows_sys_job::CloseHandle(self.handle);
        }
    }
}

// —— 最小 Windows API 声明（不引入外部 crate） ——
#[cfg(windows)]
mod windows_sys_job {
    use core::ffi::c_void;

    /// HANDLE 用 isize 表示（Windows 句柄本质是指针大小的整数；isize 可 Send/Sync）。
    pub type HANDLE = isize;
    pub type BOOL = i32;
    pub type DWORD = u32;

    pub const JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: DWORD = 0x2000;
    pub const JOB_OBJECT_EXTENDED_LIMIT_INFORMATION: i32 = 9;

    #[repr(C)]
    #[allow(non_snake_case)]
    pub struct JOBOBJECT_BASIC_LIMIT_INFORMATION {
        pub PerProcessUserTimeLimit: i64,
        pub PerJobUserTimeLimit: i64,
        pub LimitFlags: DWORD,
        pub MinimumWorkingSetSize: usize,
        pub MaximumWorkingSetSize: usize,
        pub ActiveProcessLimit: DWORD,
        pub Affinity: usize,
        pub PriorityClass: DWORD,
        pub SchedulingClass: DWORD,
    }

    #[repr(C)]
    #[allow(non_snake_case)]
    pub struct IO_COUNTERS {
        pub ReadOperationCount: u64,
        pub WriteOperationCount: u64,
        pub OtherOperationCount: u64,
        pub ReadTransferCount: u64,
        pub WriteTransferCount: u64,
        pub OtherTransferCount: u64,
    }

    #[repr(C)]
    #[allow(non_snake_case)]
    pub struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
        pub BasicLimitInformation: JOBOBJECT_BASIC_LIMIT_INFORMATION,
        pub IoInfo: IO_COUNTERS,
        pub ProcessMemoryLimit: usize,
        pub JobMemoryLimit: usize,
        pub PeakProcessMemoryUsed: usize,
        pub PeakJobMemoryUsed: usize,
    }

    unsafe extern "system" {
        pub fn CreateJobObjectW(lpJobAttributes: *const c_void, lpName: *const u16) -> HANDLE;
        pub fn SetInformationJobObject(
            hJob: HANDLE,
            JobObjectInformationClass: i32,
            lpJobObjectInformation: *const c_void,
            cbJobObjectInformationLength: DWORD,
        ) -> BOOL;
        pub fn AssignProcessToJobObject(hJob: HANDLE, hProcess: HANDLE) -> BOOL;
        pub fn CloseHandle(hObject: HANDLE) -> BOOL;
    }
}
