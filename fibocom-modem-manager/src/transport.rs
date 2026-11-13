use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream, ToSocketAddrs, UdpSocket};
use std::time::Duration;

pub enum Transport {
    Serial(Box<dyn serialport::SerialPort>),
    Tcp {
        stream: TcpStream,
    },
    Udp {
        sock: UdpSocket,
        remote: SocketAddr,
    },
}

fn serial_err(e: &serialport::Error) -> std::io::Error {
    let mut src: Option<&(dyn std::error::Error + 'static)> = std::error::Error::source(e);
    while let Some(s) = src {
        if let Some(ioe) = s.downcast_ref::<std::io::Error>() {
            return std::io::Error::new(ioe.kind(), e.to_string());
        }
        src = s.source();
    }
    std::io::Error::other(e.to_string())
}

pub fn open_serial(
    name: &str,
    baud: u32,
    parity: &str,
    stop_bits: u8,
    flow: &str,
) -> Result<Transport, String> {
    let par = match parity.to_ascii_lowercase().as_str() {
        "odd" => serialport::Parity::Odd,
        "even" => serialport::Parity::Even,
        _ => serialport::Parity::None,
    };
    let stop = match stop_bits {
        2 => serialport::StopBits::Two,
        _ => serialport::StopBits::One,
    };
    let fc = match flow.to_ascii_lowercase().as_str() {
        "hardware" => serialport::FlowControl::Hardware,
        "software" => serialport::FlowControl::Software,
        _ => serialport::FlowControl::None,
    };
    let mut port = serialport::new(name, baud)
        .data_bits(serialport::DataBits::Eight)
        .parity(par)
        .stop_bits(stop)
        .flow_control(fc)
        .timeout(Duration::from_millis(60))
        .open()
        .map_err(|e| format!("打开串口 {} 失败: {}", name, serial_err(&e)))?;
    // 部分 USB CDC-ACM AT 口需要 DTR/RTS 置位才会响应 AT
    let _ = port.write_data_terminal_ready(true);
    let _ = port.write_request_to_send(true);
    std::thread::sleep(Duration::from_millis(150));
    let _ = port.clear(serialport::ClearBuffer::All);
    Ok(Transport::Serial(port))
}

pub fn open_tcp(host: &str, port: u16) -> Result<Transport, String> {
    let addr = format!("{}:{}", host, port)
        .to_socket_addrs()
        .map_err(|e| format!("地址无效: {}", e))?
        .next()
        .ok_or_else(|| "地址无效".to_string())?;
    let stream = TcpStream::connect_timeout(&addr, Duration::from_secs(3))
        .map_err(|e| format!("连接 {} 失败: {}", addr, e))?;
    let _ = stream.set_read_timeout(Some(Duration::from_millis(60)));
    let _ = stream.set_nodelay(true);
    Ok(Transport::Tcp { stream })
}

pub fn open_udp(host: &str, port: u16) -> Result<Transport, String> {
    let addr = format!("{}:{}", host, port)
        .to_socket_addrs()
        .map_err(|e| format!("地址无效: {}", e))?
        .next()
        .ok_or_else(|| "地址无效".to_string())?;
    let sock =
        UdpSocket::bind("0.0.0.0:0").map_err(|e| format!("创建 UDP 套接字失败: {}", e))?;
    let _ = sock.set_read_timeout(Some(Duration::from_millis(80)));
    Ok(Transport::Udp { sock, remote: addr })
}

impl Transport {
    pub fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        match self {
            Transport::Serial(p) => match p.read(buf) {
                Ok(n) => Ok(n),
                Err(e) if e.kind() == std::io::ErrorKind::TimedOut => Ok(0),
                Err(e) if e.kind() == std::io::ErrorKind::Interrupted => Ok(0),
                Err(e) => Err(e),
            },
            Transport::Tcp { stream, .. } => match stream.read(buf) {
                Ok(n) => Ok(n),
                Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => Ok(0),
                Err(e) if e.kind() == std::io::ErrorKind::TimedOut => Ok(0),
                Err(e) => Err(e),
            },
            Transport::Udp { sock, remote } => match sock.recv_from(buf) {
                Ok((n, _src)) => {
                    let _ = remote;
                    Ok(n)
                }
                Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => Ok(0),
                Err(e) if e.kind() == std::io::ErrorKind::TimedOut => Ok(0),
                Err(e) => Err(e),
            },
        }
    }

    pub fn write_all(&mut self, data: &[u8]) -> std::io::Result<()> {
        match self {
            Transport::Serial(p) => p.write_all(data),
            Transport::Tcp { stream, .. } => stream.write_all(data),
            Transport::Udp { sock, remote } => sock.send_to(data, *remote).map(|_| ()),
        }
    }

    pub fn flush(&mut self) -> std::io::Result<()> {
        match self {
            Transport::Serial(p) => p.flush(),
            Transport::Tcp { stream, .. } => stream.flush(),
            Transport::Udp { .. } => Ok(()),
        }
    }

    /// 丢弃接收缓冲中的残留数据，避免上一条命令/URC 干扰。
    pub fn drain(&mut self) {
        let mut buf = [0u8; 1024];
        let mut guard = 0;
        while guard < 32 {
            match self.read(&mut buf) {
                Ok(0) => break,
                Ok(_) => guard += 1,
                Err(_) => break,
            }
        }
    }

    #[allow(dead_code)]
    pub fn describe(&self) -> String {
        match self {
            Transport::Serial(p) => format!("{} @ {}", p.name().unwrap_or_default(), p.baud_rate().unwrap_or(115200)),
            Transport::Tcp { stream, .. } => format!("TCP {}", stream.peer_addr().map(|a| a.to_string()).unwrap_or_default()),
            Transport::Udp { remote, .. } => format!("UDP {}", remote),
        }
    }
}

pub fn list_ports() -> Vec<serialport::SerialPortInfo> {
    serialport::available_ports().unwrap_or_default()
}
