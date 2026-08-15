class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "3.0.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.0/reminal_3.0.0_darwin_arm64.tar.gz"
      sha256 "4d0bd742d21a7c9f56904ab05a988b97b93498290f4657f55cf71e881e057474"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.0/reminal_3.0.0_darwin_amd64.tar.gz"
      sha256 "a1ec9632d36b3d7bfb896b2421c229f918ea5efbe3727e430c7a5d5f2b69b077"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.0/reminal_3.0.0_linux_arm64.tar.gz"
      sha256 "8203fe2b7df27bf9ea5b0caaa80ad71a4c246c1ba96a7b5b412206653226f3d3"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.0/reminal_3.0.0_linux_amd64.tar.gz"
      sha256 "663c83cffecec49e331d8012d9db108af4f9a8d5b40640ef9480c7b4aae0501c"
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
