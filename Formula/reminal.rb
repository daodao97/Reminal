class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.5"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.5/reminal_2.1.5_darwin_arm64.tar.gz"
      sha256 "9554b730a20da55d245bd2b1177b5ca53ed6a4cd5db8e8eeaee9cbcec833ce1e"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.5/reminal_2.1.5_darwin_amd64.tar.gz"
      sha256 "9156efd436f6162450e8f1d87022793503264f123c42008d440a98e98c2ad76a"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.5/reminal_2.1.5_linux_arm64.tar.gz"
      sha256 "bc97d8965755906914dfba98bbeb295e8486ce4a67ab7437cd0ece83f64fcee0"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.5/reminal_2.1.5_linux_amd64.tar.gz"
      sha256 "2cacfc9409d61b2b0d132de04290977f0d7df2a9675ed1ecfacd5473348050a5"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
