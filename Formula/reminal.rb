class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "3.0.1"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.1/reminal_3.0.1_darwin_arm64.tar.gz"
      sha256 "722896b14e624c1ac963a0d45bd9503a4da21c6e8ac4305ed2dc4703f952b8f6"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.1/reminal_3.0.1_darwin_amd64.tar.gz"
      sha256 "0d42e97040689e5cfc8f326bf07f6558a476261b472fed92cd4255ba1ec1955b"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.1/reminal_3.0.1_linux_arm64.tar.gz"
      sha256 "5b85e9b7f7702567c2fce84346312759805e403cdc6a6a184e6d15ead6eb7b3d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.1/reminal_3.0.1_linux_amd64.tar.gz"
      sha256 "244f0a1f57af9bb8a1074fd7fc4bfd460176902cf2504bfb74364239d6000aee"
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
